# 007-job-ttl.hurl
#
# Per-job TTL behavior.
#
# Covered edge cases:
#   - A pending job with expired TTL is deleted and not claimable.
#   - A reserved job whose TTL expires before visibility-timeout cleanup
#     is deleted instead of being re-queued.
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
worker_token: jsonpath "$.token"
[Asserts]
jsonpath "$.token" exists
jsonpath "$.token" isString

# ═════════════════════════════════════════════════════════════════════════════
# SECTION 1 — Expired Pending Job
# ═════════════════════════════════════════════════════════════════════════════

POST {{base_url}}/queues/{{run_id}}-ttl-pending/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "ttl": "pending" }, "ttl": 1 }
HTTP 201
[Captures]
pending_job_id: jsonpath "$.id"
[Asserts]
jsonpath "$.queue" == "{{run_id}}-ttl-pending"
jsonpath "$.status" == "pending"
jsonpath "$.ttl" == 1

GET {{base_url}}/queues/{{run_id}}-ttl-pending/next
Authorization: Bearer {{worker_token}}
[Options]
delay: 1500ms
HTTP 204

# ═════════════════════════════════════════════════════════════════════════════
# SECTION 2 — Expired Reserved Job
# ═════════════════════════════════════════════════════════════════════════════

POST {{base_url}}/queues/{{run_id}}-ttl-reserved/jobs
Content-Type: application/json
[BasicAuth]
e2e-admin:e2e-secret

{ "payload": { "ttl": "reserved" }, "ttl": 1 }
HTTP 201
[Captures]
reserved_job_id: jsonpath "$.id"
[Asserts]
jsonpath "$.queue" == "{{run_id}}-ttl-reserved"
jsonpath "$.status" == "pending"
jsonpath "$.ttl" == 1

GET {{base_url}}/queues/{{run_id}}-ttl-reserved/next
Authorization: Bearer {{worker_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{reserved_job_id}}"
jsonpath "$.payload.ttl" == "reserved"
jsonpath "$.attempts" == 1

GET {{base_url}}/queues/{{run_id}}-ttl-reserved/next
Authorization: Bearer {{worker_token}}
[Options]
delay: 3s
retry: 4
retry-interval: 500ms
HTTP 204
