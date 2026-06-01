package queue

import (
	"math"
	"sync"
	"time"

	cache "github.com/go-pkgz/expirable-cache/v3"

	"github.com/mkoziy/http-queue/config"
)

// NextPollAdvisor computes advisory worker polling intervals per queue.
type NextPollAdvisor struct {
	baseInterval   time.Duration
	minInterval    time.Duration
	maxInterval    time.Duration
	activityWindow time.Duration

	mu     sync.Mutex
	queues map[string]cache.Cache[string, struct{}]
}

// NewNextPollAdvisor creates an advisor backed by per-queue expirable caches.
func NewNextPollAdvisor(cfg *config.Config) *NextPollAdvisor {
	return &NextPollAdvisor{
		baseInterval:   cfg.WorkerNextBaseInterval,
		minInterval:    cfg.WorkerNextMinInterval,
		maxInterval:    cfg.WorkerNextMaxInterval,
		activityWindow: cfg.WorkerNextActivityWindow,
		queues:         make(map[string]cache.Cache[string, struct{}]),
	}
}

// NextPollSeconds records worker activity for the queue and returns the
// advisory delay before the worker should poll the queue again.
func (a *NextPollAdvisor) NextPollSeconds(queueName, workerID string) (int, error) {
	if err := validateQueueName(queueName); err != nil {
		return 0, err
	}

	cacheForQueue := a.queueCache(queueName)
	cacheForQueue.Set(workerID, struct{}{}, 0)
	cacheForQueue.DeleteExpired()

	activeWorkers := cacheForQueue.Len()
	if activeWorkers == 0 {
		activeWorkers = 1
	}

	nextPoll := time.Duration(math.Ceil(a.baseInterval.Seconds()*math.Sqrt(float64(activeWorkers)))) * time.Second
	if nextPoll < a.minInterval {
		nextPoll = a.minInterval
	}
	if nextPoll > a.maxInterval {
		nextPoll = a.maxInterval
	}

	if cacheForQueue.Len() == 0 {
		a.mu.Lock()
		delete(a.queues, queueName)
		a.mu.Unlock()
	}

	return int(nextPoll / time.Second), nil
}

func (a *NextPollAdvisor) queueCache(queueName string) cache.Cache[string, struct{}] {
	a.mu.Lock()
	defer a.mu.Unlock()

	if queueCache, ok := a.queues[queueName]; ok {
		return queueCache
	}

	queueCache := cache.NewCache[string, struct{}]().WithTTL(a.activityWindow)
	a.queues[queueName] = queueCache
	return queueCache
}
