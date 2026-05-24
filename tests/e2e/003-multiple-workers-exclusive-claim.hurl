# 003-multiple-workers-exclusive-claim.hurl
#
# Exclusive claim: register two workers, schedule a job, have worker 1 claim
# it, then verify that worker 2 cannot claim the same job while it is reserved
# by worker 1 (204 No Content). Then worker 1 acks the job, and both workers
# get 204 on subsequent claims from that queue.
#
# Variables (supplied by e2e-local.sh):
#   {{base_url}}  — http://127.0.0.1:<port>
#   {{run_id}}    — unique per run, e.g. "e2e-1712345678"

# ─── Step 1: Register worker 1 ──────────────────────────────────────────────
POST {{base_url}}/workers
[BasicAuth]
e2e-admin:e2e-secret
HTTP 201
[Captures]
worker1_id: jsonpath "$.worker_id"
worker1_token: jsonpath "$.token"
[Asserts]
jsonpath "$.worker_id" exists
jsonpath "$.worker_id" isString
jsonpath "$.token" exists
jsonpath "$.token" isString

# ─── Step 2: Register worker 2 ──────────────────────────────────────────────
POST {{base_url}}/workers
[BasicAuth]
e2e-admin:e2e-secret
HTTP 201
[Captures]
worker2_id: jsonpath "$.worker_id"
worker2_token: jsonpath "$.token"
[Asserts]
jsonpath "$.worker_id" exists
jsonpath "$.worker_id" isString
jsonpath "$.token" exists
jsonpath "$.token" isString
jsonpath "$.worker_id" != "{{worker1_id}}"
jsonpath "$.token" != "{{worker1_token}}"

# ─── Step 3: Schedule a job ─────────────────────────────────────────────────
POST {{base_url}}/queues/{{run_id}}-multi-worker/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "exclusive": true } }
HTTP 201
[Captures]
job_id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" exists
jsonpath "$.id" isString
jsonpath "$.queue" == "{{run_id}}-multi-worker"
jsonpath "$.status" == "pending"
jsonpath "$.created" exists

# ─── Step 4: Worker 1 claims the job ────────────────────────────────────────
# Worker 1 should receive the job because it is the first to claim it.
GET {{base_url}}/queues/{{run_id}}-multi-worker/next
Authorization: Bearer {{worker1_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id}}"
jsonpath "$.queue" == "{{run_id}}-multi-worker"
jsonpath "$.payload.exclusive" == true
jsonpath "$.attempts" == 1

# ─── Step 5: Worker 2 tries to claim the same job ───────────────────────────
# The job is reserved by worker 1 (visibility timeout is 2s), so worker 2
# should receive 204 No Content (no pending jobs available).
GET {{base_url}}/queues/{{run_id}}-multi-worker/next
Authorization: Bearer {{worker2_token}}
HTTP 204

# ─── Step 6: Worker 1 acks the job ──────────────────────────────────────────
# Worker 1 can successfully ack the job it owns.
POST {{base_url}}/jobs/{{job_id}}/ack
Authorization: Bearer {{worker1_token}}
HTTP 204

# ─── Step 7: Both workers see empty queue after ack ─────────────────────────
# After ack, no pending jobs remain in the queue.
GET {{base_url}}/queues/{{run_id}}-multi-worker/next
Authorization: Bearer {{worker1_token}}
HTTP 204

GET {{base_url}}/queues/{{run_id}}-multi-worker/next
Authorization: Bearer {{worker2_token}}
HTTP 204
