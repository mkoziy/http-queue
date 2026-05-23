# 001-happy-path.hurl
#
# Happy path: register a worker, schedule a job, claim it, ack it,
# then verify the queue is empty.
#
# Variables (supplied by e2e-local.sh):
#   {{base_url}}  — http://127.0.0.1:<port>
#   {{run_id}}    — unique per run, e.g. "e2e-1712345678"

# ─── Step 1: Register a worker ──────────────────────────────────────────────
# Admin endpoint — requires valid Basic Auth.
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

# ─── Step 2: Schedule a job ─────────────────────────────────────────────────
# Admin endpoint — requires valid Basic Auth.
POST {{base_url}}/queues/{{run_id}}-happy/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "hello": "world" } }
HTTP 201
[Captures]
job_id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" exists
jsonpath "$.id" isString
jsonpath "$.queue" == "{{run_id}}-happy"
jsonpath "$.status" == "pending"
jsonpath "$.created" exists

# ─── Step 3: Claim the job ──────────────────────────────────────────────────
# Worker endpoint — requires valid Bearer token.
GET {{base_url}}/queues/{{run_id}}-happy/next
Authorization: Bearer {{worker_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id}}"
jsonpath "$.queue" == "{{run_id}}-happy"
jsonpath "$.payload.hello" == "world"
jsonpath "$.attempts" == 1

# ─── Step 4: Ack the claimed job ────────────────────────────────────────────
POST {{base_url}}/jobs/{{job_id}}/ack
Authorization: Bearer {{worker_token}}
HTTP 204

# ─── Step 5: Queue is empty after ack ───────────────────────────────────────
# Claiming from an empty queue returns 204 No Content.
GET {{base_url}}/queues/{{run_id}}-happy/next
Authorization: Bearer {{worker_token}}
HTTP 204
