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

// Config holds all runtime configuration for the HTTP queue engine.
type Config struct {
	Port string

	AdminUser string
	AdminPass string

	BadgerPath string

	VisibilityTimeout time.Duration
	WorkerExpiry      time.Duration
	SweepInterval     time.Duration
	MaxAttempts       int
	LastSeenDebounce  time.Duration
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
		Port:              envStr("PORT", "8080"),
		AdminUser:         os.Getenv("ADMIN_USER"),
		AdminPass:         os.Getenv("ADMIN_PASS"),
		BadgerPath:        envStr("BADGER_PATH", "/tmp/http-queue"),
		VisibilityTimeout: envDuration("VISIBILITY_TIMEOUT", 30*time.Second),
		WorkerExpiry:      envDuration("WORKER_EXPIRY", 5*time.Minute),
		SweepInterval:     envDuration("SWEEP_INTERVAL", 30*time.Second),
		MaxAttempts:       envInt("MAX_ATTEMPTS", 3),
		LastSeenDebounce:  envDuration("LAST_SEEN_DEBOUNCE", 30*time.Second),
	}

	if cfg.AdminUser == "" || cfg.AdminPass == "" {
		return nil, ErrMissingAdminCredentials
	}

	if cfg.SweepInterval <= 0 {
		return nil, ErrInvalidSweepInterval
	}

	return cfg, nil
}
