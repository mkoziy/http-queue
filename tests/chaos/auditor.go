package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	badger "github.com/dgraph-io/badger/v4"
)

// dbJob is a minimal decode of the job record stored in BadgerDB.
type dbJob struct {
	ID       string `json:"id"`
	Queue    string `json:"queue"`
	Status   string `json:"status"`
	WorkerID string `json:"workerID,omitempty"`
	Attempts int    `json:"attempts"`
}

// dbState is the reconstructed snapshot of BadgerDB after the chaos run.
type dbState struct {
	jobs     map[string]*dbJob // job:{id} → job
	pending  map[string]string // "queue:pending:id" → jobID
	reserved map[string]string // "queue:reserved:id" → jobID
	dead     map[string]string // "queue:dead:id" → jobID
	workers  map[string]bool   // workerID → exists
	tokens   map[string]bool   // workertoken hash → exists
}

// violation records one invariant failure.
type violation struct {
	Rule     string `json:"rule"`
	Detail   string `json:"detail"`
	JobID    string `json:"job_id,omitempty"`
	IndexKey string `json:"index_key,omitempty"`
}

// runAudit opens BadgerDB read-only, scans all keys, checks invariants, and
// returns the number of failures. It logs each violation as a structured error.
func runAudit(badgerPath string, led *ledger, stats *counters, log *slog.Logger, events *eventWriter) (int, []violation) {
	opts := badger.DefaultOptions(badgerPath).
		WithReadOnly(true).
		WithLogger(nil)

	bdb, err := badger.Open(opts)
	if err != nil {
		log.Error("auditor: failed to open badger read-only", "err", err)
		stats.invariantFails.Add(1)
		events.Write("error", "auditor", "audit_failed", map[string]any{"err": err.Error(), "ok": false})
		return 1, []violation{{Rule: "audit open failed", Detail: err.Error()}}
	}
	defer bdb.Close()

	state, err := scanDB(bdb)
	if err != nil {
		log.Error("auditor: scan failed", "err", err)
		stats.invariantFails.Add(1)
		events.Write("error", "auditor", "audit_failed", map[string]any{"err": err.Error(), "ok": false})
		return 1, []violation{{Rule: "audit scan failed", Detail: err.Error()}}
	}

	log.Info("auditor: db scan complete",
		"jobs", len(state.jobs),
		"pending_indexes", len(state.pending),
		"reserved_indexes", len(state.reserved),
		"dead_indexes", len(state.dead),
		"workers", len(state.workers),
		"tokens", len(state.tokens),
	)

	var violations []violation

	checkInvariants(state, led, &violations, log)

	for _, v := range violations {
		stats.invariantFails.Add(1)
		data, _ := json.Marshal(v)
		log.Error("invariant violation", "report", string(data))
		events.Write("error", "auditor", "audit_failed", map[string]any{
			"job_id":    v.JobID,
			"index_key": v.IndexKey,
			"rule":      v.Rule,
			"detail":    v.Detail,
			"ok":        false,
		})
	}

	if len(violations) > 0 {
		report, _ := json.Marshal(map[string]any{
			"run_time":       time.Now().UTC(),
			"total_failures": len(violations),
			"violations":     violations,
		})
		fmt.Printf("CHAOS AUDIT FAILURE REPORT: %s\n", report)
	} else {
		log.Info("auditor: all invariants satisfied")
		events.Write("info", "auditor", "audit_passed", map[string]any{"ok": true})
	}

	return len(violations), violations
}

func scanDB(bdb *badger.DB) (*dbState, error) {
	state := &dbState{
		jobs:     make(map[string]*dbJob),
		pending:  make(map[string]string),
		reserved: make(map[string]string),
		dead:     make(map[string]string),
		workers:  make(map[string]bool),
		tokens:   make(map[string]bool),
	}

	err := bdb.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := string(item.Key())

			switch {
			case strings.HasPrefix(key, "job:"):
				jobID := strings.TrimPrefix(key, "job:")
				var j dbJob
				if err := item.Value(func(v []byte) error {
					return json.Unmarshal(v, &j)
				}); err != nil {
					return fmt.Errorf("decode job %q: %w", jobID, err)
				}
				state.jobs[jobID] = &j

			case strings.Contains(key, ":pending:"):
				// queue:{queue}:pending:{id}
				parts := strings.SplitN(key, ":pending:", 2)
				if len(parts) == 2 {
					state.pending[key] = parts[1]
				}

			case strings.Contains(key, ":reserved:"):
				parts := strings.SplitN(key, ":reserved:", 2)
				if len(parts) == 2 {
					state.reserved[key] = parts[1]
				}

			case strings.Contains(key, ":dead:"):
				parts := strings.SplitN(key, ":dead:", 2)
				if len(parts) == 2 {
					state.dead[key] = parts[1]
				}

			case strings.HasPrefix(key, "worker:"):
				workerID := strings.TrimPrefix(key, "worker:")
				state.workers[workerID] = true

			case strings.HasPrefix(key, "workertoken:"):
				hash := strings.TrimPrefix(key, "workertoken:")
				state.tokens[hash] = true
			}
		}
		return nil
	})
	return state, err
}

func checkInvariants(state *dbState, led *ledger, violations *[]violation, log *slog.Logger) {
	led.mu.Lock()
	published := make(map[string]publishedEntry, len(led.published))
	for k, v := range led.published {
		published[k] = v
	}
	acks := make(map[string]ackEntry, len(led.acks))
	for k, v := range led.acks {
		acks[k] = v
	}
	claims := make(map[string]claimEntry, len(led.claims))
	for k, v := range led.claims {
		claims[k] = v
	}
	led.mu.Unlock()

	// Build a set of all job IDs that appear in any queue index.
	indexedJobs := make(map[string][]string) // jobID → list of index keys

	for key, jobID := range state.pending {
		indexedJobs[jobID] = append(indexedJobs[jobID], key)
	}
	for key, jobID := range state.reserved {
		indexedJobs[jobID] = append(indexedJobs[jobID], key)
	}
	for key, jobID := range state.dead {
		indexedJobs[jobID] = append(indexedJobs[jobID], key)
	}

	// Invariant 1: Every published job is either ACKed or present in DB.
	for jobID := range published {
		if _, acked := acks[jobID]; acked {
			continue
		}
		if _, inDB := state.jobs[jobID]; inDB {
			continue
		}
		add(violations, "published job missing from DB and not ACKed", jobID, "")
	}

	// Invariant 2: Every queue index points to an existing job:{id} record.
	for key, jobID := range state.pending {
		if _, ok := state.jobs[jobID]; !ok {
			add(violations, "pending index points to missing job", jobID, key)
		}
	}
	for key, jobID := range state.reserved {
		if _, ok := state.jobs[jobID]; !ok {
			add(violations, "reserved index points to missing job", jobID, key)
		}
	}
	for key, jobID := range state.dead {
		if _, ok := state.jobs[jobID]; !ok {
			add(violations, "dead index points to missing job", jobID, key)
		}
	}

	// Invariants 3–5: Index status must match job status.
	for key, jobID := range state.pending {
		if j, ok := state.jobs[jobID]; ok && j.Status != "pending" {
			add(violations, fmt.Sprintf("pending index job has status=%q", j.Status), jobID, key)
		}
	}
	for key, jobID := range state.reserved {
		if j, ok := state.jobs[jobID]; ok && j.Status != "reserved" {
			add(violations, fmt.Sprintf("reserved index job has status=%q", j.Status), jobID, key)
		}
	}
	for key, jobID := range state.dead {
		if j, ok := state.jobs[jobID]; ok && j.Status != "dead" {
			add(violations, fmt.Sprintf("dead index job has status=%q", j.Status), jobID, key)
		}
	}

	// Invariant 6: No job has multiple queue indexes.
	for jobID, keys := range indexedJobs {
		if len(keys) > 1 {
			add(violations, fmt.Sprintf("job has %d queue indexes: %v", len(keys), keys), jobID, "")
		}
	}

	// Invariant 7: Every reserved job references an existing worker record.
	for _, jobID := range state.reserved {
		j, ok := state.jobs[jobID]
		if !ok {
			continue // already caught above
		}
		if j.WorkerID == "" {
			add(violations, "reserved job has empty workerID", jobID, "")
			continue
		}
		if !state.workers[j.WorkerID] {
			add(violations, fmt.Sprintf("reserved job references missing worker %q", j.WorkerID), jobID, "")
		}
	}

	// Invariant 8: Every ACK was preceded by a successful claim for the same worker.
	for jobID, ack := range acks {
		claim, ok := claims[jobID]
		if !ok {
			add(violations, "ACKed job has no claim record", jobID, "")
			continue
		}
		if claim.WorkerID != ack.WorkerID {
			add(violations, fmt.Sprintf("ACK worker %q != claim worker %q", ack.WorkerID, claim.WorkerID), jobID, "")
		}
	}

	log.Info("auditor: invariant check complete",
		"published", len(published),
		"acked", len(acks),
		"violations", len(*violations),
	)
}

func add(violations *[]violation, rule, jobID, indexKey string) {
	*violations = append(*violations, violation{Rule: rule, JobID: jobID, IndexKey: indexKey, Detail: rule})
}
