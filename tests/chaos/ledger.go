package main

import (
	"sync"
	"time"
)

// publishedEntry records a successfully published job.
type publishedEntry struct {
	ID          string
	Queue       string
	Marker      string
	PublishedAt time.Time
}

// claimEntry records a successful job claim.
type claimEntry struct {
	JobID     string
	Queue     string
	WorkerID  string
	Attempts  int
	ClaimedAt time.Time
}

// ackEntry records a successful ACK.
type ackEntry struct {
	JobID    string
	WorkerID string
	AckedAt  time.Time
}

// ledger is the thread-safe orchestrator record of chaos events.
type ledger struct {
	mu          sync.Mutex
	published   map[string]publishedEntry // keyed by job ID
	claims      map[string]claimEntry     // keyed by job ID (last claim wins on re-claim)
	acks        map[string]ackEntry       // keyed by job ID
	staleTokens []string                  // tokens from killed workers
}

func newLedger() *ledger {
	return &ledger{
		published: make(map[string]publishedEntry),
		claims:    make(map[string]claimEntry),
		acks:      make(map[string]ackEntry),
	}
}

func (l *ledger) addStaleToken(token string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.staleTokens = append(l.staleTokens, token)
}

// pickStaleTokens returns a snapshot of all stale tokens.
func (l *ledger) pickStaleTokens() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.staleTokens))
	copy(out, l.staleTokens)
	return out
}

func (l *ledger) recordPublish(id, queue, marker string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.published[id] = publishedEntry{ID: id, Queue: queue, Marker: marker, PublishedAt: at}
}

func (l *ledger) recordClaim(jobID, queue, workerID string, attempts int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.claims[jobID] = claimEntry{
		JobID:     jobID,
		Queue:     queue,
		WorkerID:  workerID,
		Attempts:  attempts,
		ClaimedAt: time.Now(),
	}
}

func (l *ledger) recordACK(jobID, workerID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.acks[jobID] = ackEntry{JobID: jobID, WorkerID: workerID, AckedAt: time.Now()}
}

// publishedCount returns the number of recorded published jobs.
func (l *ledger) publishedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.published)
}

// ackedCount returns the number of jobs ACKed by the orchestrator.
func (l *ledger) ackedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.acks)
}

// claimFor returns the most recent claim entry for a job, and whether it exists.
func (l *ledger) claimFor(jobID string) (claimEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.claims[jobID]
	return e, ok
}
