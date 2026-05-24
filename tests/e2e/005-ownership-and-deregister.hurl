# 005-ownership-and-deregister.hurl
#
# Ownership enforcement and worker deregistration requeue.
#
# Covered edge cases:
#   - A worker cannot ack a job owned by another worker.
#   - A worker cannot nack a job owned by another worker.
#   - Deregistering a worker requeues its reserved jobs.
#
# Variables (supplied by e2e-local.sh):
#   {{base_url}}  — http://127.0.0.1:<port>
#   {{run_id}}    — unique per run, e.g. "e2e-1712345678"

# ─── Setup: Register two workers ────────────────────────────────────────────
POST {{base_url}}/workers
[BasicAuth]
e2e-admin:e2e-secret
HTTP 201
[Captures]
worker_a_id: jsonpath "$.worker_id"
worker_a_token: jsonpath "$.token"
[Asserts]
jsonpath "$.worker_id" exists
jsonpath "$.worker_id" isString
jsonpath "$.token" exists
jsonpath "$.token" isString

POST {{base_url}}/workers
[BasicAuth]
e2e-admin:e2e-secret
HTTP 201
[Captures]
worker_b_id: jsonpath "$.worker_id"
worker_b_token: jsonpath "$.token"
[Asserts]
jsonpath "$.worker_id" exists
jsonpath "$.worker_id" isString
jsonpath "$.token" exists
jsonpath "$.token" isString
jsonpath "$.worker_id" != "{{worker_a_id}}"
jsonpath "$.token" != "{{worker_a_token}}"

# ═════════════════════════════════════════════════════════════════════════════
# SECTION 1 — Ownership Enforcement
# ═════════════════════════════════════════════════════════════════════════════
# A worker cannot ack or nack a job that is reserved by another worker.

# ─── 1A: Schedule a job ──────────────────────────────────────────────────────
POST {{base_url}}/queues/{{run_id}}-ownership/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "ownership": true } }
HTTP 201
[Captures]
job_id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" exists
jsonpath "$.id" isString
jsonpath "$.queue" == "{{run_id}}-ownership"
jsonpath "$.status" == "pending"

# ─── 1B: Worker A claims the job ─────────────────────────────────────────────
GET {{base_url}}/queues/{{run_id}}-ownership/next
Authorization: Bearer {{worker_a_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id}}"
jsonpath "$.queue" == "{{run_id}}-ownership"
jsonpath "$.payload.ownership" == true
jsonpath "$.attempts" == 1

# ─── 1C: Worker B tries to ack → 400 (not owner) ────────────────────────────
POST {{base_url}}/jobs/{{job_id}}/ack
Authorization: Bearer {{worker_b_token}}
HTTP 400
[Asserts]
jsonpath "$.error" contains "job not owned by this worker"

# ─── 1D: Worker B tries to nack → 400 (not owner) ───────────────────────────
POST {{base_url}}/jobs/{{job_id}}/nack
Authorization: Bearer {{worker_b_token}}
HTTP 400
[Asserts]
jsonpath "$.error" contains "job not owned by this worker"

# ─── 1E: Worker A acks the job (cleanup) ────────────────────────────────────
POST {{base_url}}/jobs/{{job_id}}/ack
Authorization: Bearer {{worker_a_token}}
HTTP 204

# ═════════════════════════════════════════════════════════════════════════════
# SECTION 2 — Deregister Worker Requeues Reserved Jobs
# ═════════════════════════════════════════════════════════════════════════════
# When a worker is deregistered, any jobs it has reserved are automatically
# returned to the pending queue so another worker can claim them.

# ─── 2A: Schedule a job ──────────────────────────────────────────────────────
POST {{base_url}}/queues/{{run_id}}-deregister/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "deregister": true } }
HTTP 201
[Captures]
job_id2: jsonpath "$.id"
[Asserts]
jsonpath "$.id" exists
jsonpath "$.id" isString
jsonpath "$.queue" == "{{run_id}}-deregister"
jsonpath "$.status" == "pending"

# ─── 2B: Worker A claims the job ─────────────────────────────────────────────
GET {{base_url}}/queues/{{run_id}}-deregister/next
Authorization: Bearer {{worker_a_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id2}}"
jsonpath "$.queue" == "{{run_id}}-deregister"
jsonpath "$.payload.deregister" == true
jsonpath "$.attempts" == 1

# ─── 2C: Deregister worker A — requeues its reserved job ────────────────────
DELETE {{base_url}}/workers/{{worker_a_id}}
[BasicAuth]
e2e-admin:e2e-secret
HTTP 204

# ─── 2D: Worker B claims the same job (now pending after deregister) ────────
# Attempts count is preserved across re-queues and incremented on re-claim.
GET {{base_url}}/queues/{{run_id}}-deregister/next
Authorization: Bearer {{worker_b_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{job_id2}}"
jsonpath "$.queue" == "{{run_id}}-deregister"
jsonpath "$.payload.deregister" == true
jsonpath "$.attempts" == 2

# ─── 2E: Worker B acks the job ──────────────────────────────────────────────
POST {{base_url}}/jobs/{{job_id2}}/ack
Authorization: Bearer {{worker_b_token}}
HTTP 204
