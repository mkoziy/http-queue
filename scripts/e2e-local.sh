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

# --- Create a port communication file (Go server writes the actual port here when using :0) ---
PORT_FILE="$(mktemp /tmp/http-queue-port.XXXXXX)"
echo "Port file: $PORT_FILE"

# --- Create temporary BadgerDB directory ---
BADGER_TMPDIR="$(mktemp -d /tmp/http-queue-e2e.XXXXXX)"
echo "BadgerDB temp dir: $BADGER_TMPDIR"

# --- Generate a unique run_id ---
run_id="e2e-$(date +%s)"
echo "Run ID: $run_id"

# --- E2E-specific config (fast timings, deterministic credentials) ---
export ADMIN_USER="e2e-admin"
export ADMIN_PASS="e2e-secret"
export PORT="0"
export PORT_FILE="$PORT_FILE"
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
  if [ -n "${PORT_FILE:-}" ] && [ -f "$PORT_FILE" ]; then
    rm -f "$PORT_FILE"
    echo "Port file removed: $PORT_FILE"
  fi
  if [ -d "$BADGER_TMPDIR" ]; then
    rm -rf "$BADGER_TMPDIR"
    echo "Temp BadgerDB directory removed: $BADGER_TMPDIR"
  fi
  exit "$exit_code"
}
trap cleanup EXIT INT TERM

# --- Build and start the server ---
echo "Building server..."
cd "$(dirname "$0")/.."
BIN_PATH="$BADGER_TMPDIR/http-queue-e2e"
go build -o "$BIN_PATH" .
echo "Starting server..."
"$BIN_PATH" &
SERVER_PID=$!
echo "Server PID: $SERVER_PID"

# --- Wait for the server to write its actual port ---
echo -n "Waiting for server to report port..."
ACTUAL_PORT=""
for i in $(seq 1 30); do
  if [ -f "$PORT_FILE" ]; then
    ACTUAL_PORT=$(cat "$PORT_FILE" 2>/dev/null || true)
    if [ -n "$ACTUAL_PORT" ]; then
      echo " port $ACTUAL_PORT (attempt $i)"
      break
    fi
  fi
  if [ "$i" -eq 30 ]; then
    echo ""
    echo "ERROR: Server did not report a port within 30 attempts."
    exit 1
  fi
  sleep 0.5
done

# Override PORT with the actual port the server is listening on
PORT="$ACTUAL_PORT"

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

# Use nullglob so unmatched globs produce an empty array
shopt -s nullglob
HURL_FILES=("$HURL_DIR"/*.hurl)
shopt -u nullglob
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
