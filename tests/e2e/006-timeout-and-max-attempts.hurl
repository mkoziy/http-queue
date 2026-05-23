# 006-timeout-and-max-attempts.hurl
#
# Visibility timeout requeue and max-attempts dead-letter behavior.
#
# Covered edge cases:
#   - A reserved job is requeued when the visibility timeout expires
#     (the sweeper returns it to the pending queue).
#   - Nack at MAX_ATTEMPTS (3 in e2e config) moves the job to dead-letter
#     status, removing it from the normal claim flow.
#
# Variables (supplied by e2e-local.sh):
#   {{base_url}}  — http://127.0.0.1:<port>
#   {{run_id}}    — unique per run, e.g. "e2e-1712345678"

# ─── Setup: Register a worker ────────────────────────────────────────────
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
# SECTION 1 — Visibility Timeout Requeue
# ═════════════════════════════════════════════════════════════════════════════
# After a job is claimed (reserved), if the worker does not ack or nack
# within the visibility timeout (2s in e2e config), the sweeper returns
# the job to the pending queue so another worker can claim it.

# ─── 1A: Schedule a job ──────────────────────────────────────────────────────
POST {{base_url}}/queues/{{run_id}}-timeout/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "timeout": true } }
HTTP 201
[Captures]
job_id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" exists
jsonpath "$.id" isString
jsonpath "$.queue" == "{{run_id}}-timeout"
jsonpath "$.status" == "pending"

# ─── 1B: Claim the job (attempts=1) ──────────────────────────────────────────
# The job transitions from pending to reserved with a 2s visibility timeout.
GET {{base_url}}/queues/{{run_id}}-timeout/next
Authorization: Bearer {{worker_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id}}"
jsonpath "$.queue" == "{{run_id}}-timeout"
jsonpath "$.payload.timeout" == true
jsonpath "$.attempts" == 1

# ─── 1C: Wait for visibility timeout, then claim again ──────────────────────
# The visibility timeout is 2s and the sweeper runs every 1s, so after ~3s
# the job should be requeued as pending. We retry with 500ms intervals up to
# 10 times (total ~5s) until we get a 200 response.
GET {{base_url}}/queues/{{run_id}}-timeout/next
Authorization: Bearer {{worker_token}}
[Options]
retry: 10
retry-interval: 500
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id}}"
jsonpath "$.queue" == "{{run_id}}-timeout"
jsonpath "$.payload.timeout" == true
jsonpath "$.attempts" == 2

# ─── 1D: Ack the job ────────────────────────────────────────────────────────
POST {{base_url}}/jobs/{{job_id}}/ack
Authorization: Bearer {{worker_token}}
HTTP 204

# ═════════════════════════════════════════════════════════════════════════════
# SECTION 2 — Max Attempts Dead-Letter (via nack)
# ═════════════════════════════════════════════════════════════════════════════
# When a job reaches MAX_ATTEMPTS (3 in e2e config) it is moved to dead-letter
# status and can no longer be claimed through the normal queue flow.

# ─── 2A: Schedule a job ──────────────────────────────────────────────────────
POST {{base_url}}/queues/{{run_id}}-max-attempts/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "maxattempts": true } }
HTTP 201
[Captures]
job_id2: jsonpath "$.id"
[Asserts]
jsonpath "$.id" exists
jsonpath "$.id" isString
jsonpath "$.queue" == "{{run_id}}-max-attempts"
jsonpath "$.status" == "pending"

# ─── 2B: Claim #1 (attempts=1) and nack → requeued ──────────────────────────
# After the first claim, attempts=1. Nack requeues it because 1 < 3.
GET {{base_url}}/queues/{{run_id}}-max-attempts/next
Authorization: Bearer {{worker_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id2}}"
jsonpath "$.queue" == "{{run_id}}-max-attempts"
jsonpath "$.payload.maxattempts" == true
jsonpath "$.attempts" == 1

POST {{base_url}}/jobs/{{job_id2}}/nack
Authorization: Bearer {{worker_token}}
HTTP 204

# ─── 2C: Claim #2 (attempts=2) and nack → requeued ──────────────────────────
# After the second claim, attempts=2. Nack requeues it because 2 < 3.
GET {{base_url}}/queues/{{run_id}}-max-attempts/next
Authorization: Bearer {{worker_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id2}}"
jsonpath "$.queue" == "{{run_id}}-max-attempts"
jsonpath "$.payload.maxattempts" == true
jsonpath "$.attempts" == 2

POST {{base_url}}/jobs/{{job_id2}}/nack
Authorization: Bearer {{worker_token}}
HTTP 204

# ─── 2D: Claim #3 (attempts=3) and nack → dead-letter ───────────────────────
# After the third claim, attempts=3. Nack moves it to dead-letter because
# attempts (3) >= MAX_ATTEMPTS (3).
GET {{base_url}}/queues/{{run_id}}-max-attempts/next
Authorization: Bearer {{worker_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id2}}"
jsonpath "$.queue" == "{{run_id}}-max-attempts"
jsonpath "$.payload.maxattempts" == true
jsonpath "$.attempts" == 3

POST {{base_url}}/jobs/{{job_id2}}/nack
Authorization: Bearer {{worker_token}}
HTTP 204

# ─── 2E: Queue is empty after dead-letter ────────────────────────────────────
# The job is in dead-letter status and has no pending index entry, so it
# is no longer claimable. The queue appears empty.
GET {{base_url}}/queues/{{run_id}}-max-attempts/next
Authorization: Bearer {{worker_token}}
HTTP 204
