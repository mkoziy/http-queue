# 004-auth-and-validation.hurl
#
# Auth and validation edge cases: missing/wrong Basic Auth credentials,
# missing/invalid Bearer tokens, invalid request bodies, invalid queue
# names, and empty queue behavior.
#
# Variables (supplied by e2e-local.sh):
#   {{base_url}}  — http://127.0.0.1:<port>
#   {{run_id}}    — unique per run, e.g. "e2e-1712345678"

# ─── Setup: Register a worker for downstream tests ──────────────────────────
POST {{base_url}}/workers
[BasicAuth]
e2e-admin:e2e-secret
HTTP 201
[Captures]
worker_id: jsonpath "$.worker_id"
worker_token: jsonpath "$.token"
[Asserts]
jsonpath "$.worker_id" exists
jsonpath "$.worker_id" isString
jsonpath "$.token" exists
jsonpath "$.token" isString

# ═════════════════════════════════════════════════════════════════════════════
# SECTION 1 — Admin Basic Auth Tests
# ═════════════════════════════════════════════════════════════════════════════

# ─── 1A: POST /workers without Basic Auth → 401 ─────────────────────────────
POST {{base_url}}/workers
HTTP 401

# ─── 1B: POST /workers with wrong Basic Auth → 401 ──────────────────────────
POST {{base_url}}/workers
[BasicAuth]
wrong:creds
HTTP 401

# ─── 1C: POST /queues/{queue}/jobs without Basic Auth → 401 ─────────────────
POST {{base_url}}/queues/{{run_id}}-auth/jobs
Content-Type: application/json

{ "payload": "test" }
HTTP 401

# ─── 1D: POST /queues/{queue}/jobs with wrong Basic Auth → 401 ──────────────
POST {{base_url}}/queues/{{run_id}}-auth/jobs
Content-Type: application/json
[BasicAuth]
bad:auth

{ "payload": "test" }
HTTP 401

# ─── 1E: DELETE /workers/{id} without Basic Auth → 401 ──────────────────────
DELETE {{base_url}}/workers/{{worker_id}}
HTTP 401

# ─── 1F: DELETE /workers/{id} with wrong Basic Auth → 401 ───────────────────
DELETE {{base_url}}/workers/{{worker_id}}
[BasicAuth]
bad:auth
HTTP 401

# ═════════════════════════════════════════════════════════════════════════════
# SECTION 2 — Worker Bearer Token Tests
# ═════════════════════════════════════════════════════════════════════════════

# ─── 2A: GET /queues/{queue}/next without Bearer → 401 ──────────────────────
GET {{base_url}}/queues/{{run_id}}-auth/next
HTTP 401

# ─── 2B: GET /queues/{queue}/next with empty Bearer → 401 ───────────────────
GET {{base_url}}/queues/{{run_id}}-auth/next
Authorization: Bearer 
HTTP 401

# ─── 2C: GET /queues/{queue}/next with invalid Bearer → 401 ─────────────────
GET {{base_url}}/queues/{{run_id}}-auth/next
Authorization: Bearer totally-fake-token
HTTP 401

# ─── 2D: POST /jobs/{id}/ack without Bearer → 401 ───────────────────────────
POST {{base_url}}/jobs/nonexistent/ack
HTTP 401

# ─── 2E: POST /jobs/{id}/nack without Bearer → 401 ──────────────────────────
POST {{base_url}}/jobs/nonexistent/nack
HTTP 401

# ─── 2F: POST /jobs/{id}/ack with invalid Bearer → 401 ──────────────────────
POST {{base_url}}/jobs/nonexistent/ack
Authorization: Bearer bogus-token
HTTP 401

# ─── 2G: POST /jobs/{id}/nack with invalid Bearer → 401 ─────────────────────
POST {{base_url}}/jobs/nonexistent/nack
Authorization: Bearer bogus-token
HTTP 401

# ═════════════════════════════════════════════════════════════════════════════
# SECTION 3 — Input Validation
# ═════════════════════════════════════════════════════════════════════════════

# ─── 3A: Invalid JSON body → 400 ────────────────────────────────────────────
# Use a file body so Hurl sends raw bytes without parsing them as JSON.
# The file is resolved relative to the Hurl file's directory.
POST {{base_url}}/queues/{{run_id}}-auth/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret
file,invalid-body.bin;
HTTP 400
[Asserts]
jsonpath "$.error" == "invalid request body"

# ─── 3B: Queue name with ':' → 400 ──────────────────────────────────────────
POST {{base_url}}/queues/{{run_id}}-bad:queue/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "x": 1 } }
HTTP 400
[Asserts]
jsonpath "$.error" == "queue name must not contain ':' or '/'"

# ─── 3C: Queue name with '/' → 404 ──────────────────────────────────────────
# The Go 1.22+ HTTP mux treats '/' as a path separator, so a queue name
# containing '/' breaks the route pattern /queues/{queue}/jobs and the
# router returns 404 before reaching the handler. This is a defense-in-depth
# property — the queue validation in the handler never needs to reject '/'.
POST {{base_url}}/queues/{{run_id}}-bad/queue/jobs
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "x": 1 } }
HTTP 404

# ═════════════════════════════════════════════════════════════════════════════
# SECTION 4 — Empty Queue Behavior
# ═════════════════════════════════════════════════════════════════════════════

# ─── 4A: Claim from empty queue returns 204 ────────────────────────────────
GET {{base_url}}/queues/{{run_id}}-empty-queue/next
Authorization: Bearer {{worker_token}}
HTTP 204


