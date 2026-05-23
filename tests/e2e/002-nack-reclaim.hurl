# 002-nack-reclaim.hurl
#
# Nack and reclaim: schedule a job, claim it, nack it (returning it to the
# pending queue), claim it again, ack it, then verify the queue is empty.
#
# Variables (supplied by e2e-local.sh):
#   {{base_url}}  — http://127.0.0.1:<port>
#   {{run_id}}    — unique per run, e.g. "e2e-1712345678"

# ─── Step 1: Register a worker ──────────────────────────────────────────────
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
POST {{base_url}}/queues/{{run_id}}-nack/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "nack": "reclaim" } }
HTTP 201
[Captures]
job_id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" exists
jsonpath "$.id" isString
jsonpath "$.queue" == "{{run_id}}-nack"
jsonpath "$.status" == "pending"
jsonpath "$.created" exists

# ─── Step 3: Claim the job for the first time (attempts=1) ──────────────────
GET {{base_url}}/queues/{{run_id}}-nack/next
Authorization: Bearer {{worker_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id}}"
jsonpath "$.queue" == "{{run_id}}-nack"
jsonpath "$.payload.nack" == "reclaim"
jsonpath "$.attempts" == 1

# ─── Step 4: Nack the claimed job → requeued as pending ─────────────────────
# Nack before MAX_ATTEMPTS returns the job to the pending queue for
# another worker to claim.
POST {{base_url}}/jobs/{{job_id}}/nack
Authorization: Bearer {{worker_token}}
HTTP 204

# ─── Step 5: Claim the same job again (attempts=2) ─────────────────────────
# After nack the job is pending and can be claimed again by any worker.
# The attempt counter is incremented on each claim.
GET {{base_url}}/queues/{{run_id}}-nack/next
Authorization: Bearer {{worker_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id}}"
jsonpath "$.queue" == "{{run_id}}-nack"
jsonpath "$.payload.nack" == "reclaim"
jsonpath "$.attempts" == 2

# ─── Step 6: Ack the reclaimed job ──────────────────────────────────────────
POST {{base_url}}/jobs/{{job_id}}/ack
Authorization: Bearer {{worker_token}}
HTTP 204

# ─── Step 7: Queue is empty after ack ───────────────────────────────────────
GET {{base_url}}/queues/{{run_id}}-nack/next
Authorization: Bearer {{worker_token}}
HTTP 204
