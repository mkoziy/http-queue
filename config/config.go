// Package config provides runtime configuration loaded from environment variables.
package config

import (
	"errors"
	"os"
	"strconv"
	"time"
)

// ErrMissingAdminCredentials is returned when ADMIN_USER or ADMIN_PASS is not set.
var ErrMissingAdminCredentials = errors.New("ADMIN_USER and ADMIN_PASS must be set")

// ErrInvalidSweepInterval is returned when SWEEP_INTERVAL is not positive.
var ErrInvalidSweepInterval = errors.New("SWEEP_INTERVAL must be greater than 0")

// Worker-next polling configuration validation errors.
var (
	ErrInvalidWorkerNextBaseInterval     = errors.New("WORKER_NEXT_BASE_INTERVAL must be greater than 0")
	ErrInvalidWorkerNextMinInterval      = errors.New("WORKER_NEXT_MIN_INTERVAL must be greater than 0")
	ErrInvalidWorkerNextMaxInterval      = errors.New("WORKER_NEXT_MAX_INTERVAL must be greater than 0")
	ErrInvalidWorkerNextActivityWindow   = errors.New("WORKER_NEXT_ACTIVITY_WINDOW must be greater than 0")
	ErrInvalidWorkerNextIntervalRange    = errors.New("WORKER_NEXT_MIN_INTERVAL must be less than or equal to WORKER_NEXT_MAX_INTERVAL")
	ErrInvalidWorkerNextActivityCoverage = errors.New("WORKER_NEXT_ACTIVITY_WINDOW must be greater than or equal to WORKER_NEXT_MAX_INTERVAL")
)

// Config holds all runtime configuration for the HTTP queue engine.
type Config struct {
	Port string

	AdminUser string
	AdminPass string

	BadgerPath string

	VisibilityTimeout        time.Duration
	WorkerExpiry             time.Duration
	SweepInterval            time.Duration
	MaxAttempts              int
	LastSeenDebounce         time.Duration
	WorkerNextBaseInterval   time.Duration
	WorkerNextMinInterval    time.Duration
	WorkerNextMaxInterval    time.Duration
	WorkerNextActivityWindow time.Duration
}

func envStr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return fallback
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	cfg := &Config{
		Port:                     envStr("PORT", "8080"),
		AdminUser:                os.Getenv("ADMIN_USER"),
		AdminPass:                os.Getenv("ADMIN_PASS"),
		BadgerPath:               envStr("BADGER_PATH", "/tmp/http-queue"),
		VisibilityTimeout:        envDuration("VISIBILITY_TIMEOUT", 30*time.Second),
		WorkerExpiry:             envDuration("WORKER_EXPIRY", 5*time.Minute),
		SweepInterval:            envDuration("SWEEP_INTERVAL", 30*time.Second),
		MaxAttempts:              envInt("MAX_ATTEMPTS", 3),
		LastSeenDebounce:         envDuration("LAST_SEEN_DEBOUNCE", 30*time.Second),
		WorkerNextBaseInterval:   envDuration("WORKER_NEXT_BASE_INTERVAL", 5*time.Second),
		WorkerNextMinInterval:    envDuration("WORKER_NEXT_MIN_INTERVAL", 1*time.Second),
		WorkerNextMaxInterval:    envDuration("WORKER_NEXT_MAX_INTERVAL", 1*time.Minute),
		WorkerNextActivityWindow: envDuration("WORKER_NEXT_ACTIVITY_WINDOW", 1*time.Minute),
	}

	if cfg.AdminUser == "" || cfg.AdminPass == "" {
		return nil, ErrMissingAdminCredentials
	}

	if cfg.SweepInterval <= 0 {
		return nil, ErrInvalidSweepInterval
	}

	if cfg.WorkerNextBaseInterval <= 0 {
		return nil, ErrInvalidWorkerNextBaseInterval
	}
	if cfg.WorkerNextMinInterval <= 0 {
		return nil, ErrInvalidWorkerNextMinInterval
	}
	if cfg.WorkerNextMaxInterval <= 0 {
		return nil, ErrInvalidWorkerNextMaxInterval
	}
	if cfg.WorkerNextActivityWindow <= 0 {
		return nil, ErrInvalidWorkerNextActivityWindow
	}
	if cfg.WorkerNextMinInterval > cfg.WorkerNextMaxInterval {
		return nil, ErrInvalidWorkerNextIntervalRange
	}
	if cfg.WorkerNextActivityWindow < cfg.WorkerNextMaxInterval {
		return nil, ErrInvalidWorkerNextActivityCoverage
	}

	return cfg, nil
}
