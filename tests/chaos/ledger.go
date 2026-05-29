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

// ledger is the thread-safe orchestrator record of chaos events.
// Claim/ACK/NACK fields are added in Task 8.
type ledger struct {
	mu        sync.Mutex
	published map[string]publishedEntry // keyed by job ID
}

func newLedger() *ledger {
	return &ledger{
		published: make(map[string]publishedEntry),
	}
}

func (l *ledger) recordPublish(id, queue, marker string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.published[id] = publishedEntry{ID: id, Queue: queue, Marker: marker, PublishedAt: at}
}

// publishedCount returns the number of recorded published jobs.
func (l *ledger) publishedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.published)
}
