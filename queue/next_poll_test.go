package queue

import (
	"errors"
	"testing"
	"time"

	"github.com/mkoziy/http-queue/config"
)

func TestNextPollAdvisor_NextPollSeconds(t *testing.T) {
	cfg := &config.Config{
		WorkerNextBaseInterval:   5 * time.Second,
		WorkerNextMinInterval:    1 * time.Second,
		WorkerNextMaxInterval:    1 * time.Minute,
		WorkerNextActivityWindow: 1 * time.Minute,
	}
	advisor := NewNextPollAdvisor(cfg)

	seconds, err := advisor.NextPollSeconds("orders", "worker-1")
	if err != nil {
		t.Fatalf("NextPollSeconds() error: %v", err)
	}
	if seconds != 5 {
		t.Fatalf("worker 1 seconds = %d, want 5", seconds)
	}

	seconds, err = advisor.NextPollSeconds("orders", "worker-2")
	if err != nil {
		t.Fatalf("NextPollSeconds() second worker error: %v", err)
	}
	if seconds != 8 {
		t.Fatalf("worker 2 seconds = %d, want 8", seconds)
	}

	seconds, err = advisor.NextPollSeconds("other", "worker-3")
	if err != nil {
		t.Fatalf("other queue error: %v", err)
	}
	if seconds != 5 {
		t.Fatalf("other queue seconds = %d, want 5", seconds)
	}
}

func TestNextPollAdvisor_ClampsToMinAndMax(t *testing.T) {
	minCfg := &config.Config{
		WorkerNextBaseInterval:   500 * time.Millisecond,
		WorkerNextMinInterval:    2 * time.Second,
		WorkerNextMaxInterval:    1 * time.Minute,
		WorkerNextActivityWindow: 1 * time.Minute,
	}
	minAdvisor := NewNextPollAdvisor(minCfg)

	seconds, err := minAdvisor.NextPollSeconds("orders", "worker-1")
	if err != nil {
		t.Fatalf("min clamp error: %v", err)
	}
	if seconds != 2 {
		t.Fatalf("min clamp seconds = %d, want 2", seconds)
	}

	maxCfg := &config.Config{
		WorkerNextBaseInterval:   30 * time.Second,
		WorkerNextMinInterval:    1 * time.Second,
		WorkerNextMaxInterval:    45 * time.Second,
		WorkerNextActivityWindow: 1 * time.Minute,
	}
	maxAdvisor := NewNextPollAdvisor(maxCfg)

	for i := 0; i < 4; i++ {
		if _, err := maxAdvisor.NextPollSeconds("orders", "worker-"+string(rune('1'+i))); err != nil {
			t.Fatalf("max clamp iteration %d error: %v", i, err)
		}
	}

	seconds, err = maxAdvisor.NextPollSeconds("orders", "worker-5")
	if err != nil {
		t.Fatalf("max clamp final error: %v", err)
	}
	if seconds != 45 {
		t.Fatalf("max clamp seconds = %d, want 45", seconds)
	}
}

func TestNextPollAdvisor_ExpiresInactiveWorkers(t *testing.T) {
	cfg := &config.Config{
		WorkerNextBaseInterval:   4 * time.Second,
		WorkerNextMinInterval:    1 * time.Second,
		WorkerNextMaxInterval:    1 * time.Minute,
		WorkerNextActivityWindow: 20 * time.Millisecond,
	}
	advisor := NewNextPollAdvisor(cfg)

	if _, err := advisor.NextPollSeconds("orders", "worker-1"); err != nil {
		t.Fatalf("seed worker-1: %v", err)
	}
	if _, err := advisor.NextPollSeconds("orders", "worker-2"); err != nil {
		t.Fatalf("seed worker-2: %v", err)
	}

	time.Sleep(30 * time.Millisecond)

	seconds, err := advisor.NextPollSeconds("orders", "worker-3")
	if err != nil {
		t.Fatalf("post-expiry error: %v", err)
	}
	if seconds != 4 {
		t.Fatalf("seconds after expiry = %d, want 4", seconds)
	}
}

func TestNextPollAdvisor_InvalidQueueName(t *testing.T) {
	cfg := &config.Config{
		WorkerNextBaseInterval:   5 * time.Second,
		WorkerNextMinInterval:    1 * time.Second,
		WorkerNextMaxInterval:    1 * time.Minute,
		WorkerNextActivityWindow: 1 * time.Minute,
	}
	advisor := NewNextPollAdvisor(cfg)

	_, err := advisor.NextPollSeconds("bad/queue", "worker-1")
	if err == nil {
		t.Fatal("NextPollSeconds() expected error, got nil")
	}
	if !errors.Is(err, ErrInvalidQueueName) {
		t.Fatalf("NextPollSeconds() error = %v, want %v", err, ErrInvalidQueueName)
	}
}
