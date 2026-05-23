#!/usr/bin/env bash
set -euo pipefail

# ──────────────────────────────────────────────
# e2e-local.sh — Run Hurl E2E tests against a
# local Go server with isolated temporary state.
# ──────────────────────────────────────────────

# --- Prerequisite check ---
if ! command -v hurl &>/dev/null; then
  echo "ERROR: 'hurl' is required for E2E tests but is not installed."
  echo "Install it via: brew install hurl  (macOS) or see https://hurl.dev"
  exit 1
fi

# --- Find a free port ---
find_free_port() {
  python3 -c "import socket; s=socket.socket(); s.bind(('', 0)); print(s.getsockname()[1]); s.close()"
}
PORT="$(find_free_port)"
echo "Using port: $PORT"

# --- Create temporary BadgerDB directory ---
BADGER_TMPDIR="$(mktemp -d /tmp/http-queue-e2e.XXXXXX)"
echo "BadgerDB temp dir: $BADGER_TMPDIR"

# --- Generate a unique run_id ---
run_id="e2e-$(date +%s)"
echo "Run ID: $run_id"

# --- E2E-specific config (fast timings, deterministic credentials) ---
export ADMIN_USER="e2e-admin"
export ADMIN_PASS="e2e-secret"
export PORT="$PORT"
export BADGER_PATH="$BADGER_TMPDIR"
export VISIBILITY_TIMEOUT="2s"
export WORKER_EXPIRY="5s"
export SWEEP_INTERVAL="1s"
export MAX_ATTEMPTS="3"
export LAST_SEEN_DEBOUNCE="100ms"

# --- Cleanup handler ---
cleanup() {
  local exit_code=$?
  echo ""
  echo "Cleaning up..."
  if [ -n "${SERVER_PID:-}" ]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    echo "Server process (PID $SERVER_PID) stopped."
  fi
  if [ -d "$BADGER_TMPDIR" ]; then
    rm -rf "$BADGER_TMPDIR"
    echo "Temp BadgerDB directory removed: $BADGER_TMPDIR"
  fi
  exit "$exit_code"
}
trap cleanup EXIT INT TERM

# --- Start the server ---
echo "Starting server..."
cd "$(dirname "$0")/.."
go run . &
SERVER_PID=$!
echo "Server PID: $SERVER_PID"

# --- Wait for server readiness ---
echo -n "Waiting for server to be ready..."
for i in $(seq 1 30); do
  # Unauthenticated POST /workers returns 401 when the server is reachable.
  status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://127.0.0.1:$PORT/workers" 2>/dev/null || true)
  if [ "$status" = "401" ]; then
    echo " ready (attempt $i)"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo ""
    echo "ERROR: Server failed to become ready within 30 attempts."
    exit 1
  fi
  sleep 0.5
done

# --- Determine Hurl test files ---
HURL_DIR="tests/e2e"
if [ ! -d "$HURL_DIR" ]; then
  echo "ERROR: Hurl test directory '$HURL_DIR' not found."
  exit 1
fi

HURL_FILES=("$HURL_DIR"/*.hurl)
if [ ${#HURL_FILES[@]} -eq 0 ]; then
  echo "ERROR: No *.hurl files found in '$HURL_DIR'."
  exit 1
fi

# --- Run Hurl tests ---
echo ""
echo "Running E2E tests..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
hurl --test \
  --variable "base_url=http://127.0.0.1:$PORT" \
  --variable "run_id=$run_id" \
  "${HURL_FILES[@]}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "All E2E tests passed!"
