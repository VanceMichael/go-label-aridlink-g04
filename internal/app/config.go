package app

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Address           string
	DatabaseURL       string
	MigrationDir      string
	SessionTTL        time.Duration
	ShutdownTimeout   time.Duration
	WorkerInterval    time.Duration
	WorkerLease       time.Duration
	WorkerBatchSize   int
	BootstrapEmail    string
	BootstrapPassword string
}

func LoadConfig() (Config, error) {
	cfg := Config{
		Address:           env("ARIDLINK_ADDRESS", ":8080"),
		DatabaseURL:       env("ARIDLINK_DATABASE_URL", "postgres://aridlink:aridlink@localhost:5432/aridlink?sslmode=disable"),
		MigrationDir:      env("ARIDLINK_MIGRATIONS", "migrations"),
		SessionTTL:        envDuration("ARIDLINK_SESSION_TTL", 8*time.Hour),
		ShutdownTimeout:   envDuration("ARIDLINK_SHUTDOWN_TIMEOUT", 10*time.Second),
		WorkerInterval:    envDuration("ARIDLINK_WORKER_INTERVAL", 2*time.Second),
		WorkerLease:       envDuration("ARIDLINK_WORKER_LEASE", 30*time.Second),
		WorkerBatchSize:   envInt("ARIDLINK_WORKER_BATCH", 20),
		BootstrapEmail:    env("ARIDLINK_BOOTSTRAP_EMAIL", "admin@aridlink.local"),
		BootstrapPassword: env("ARIDLINK_BOOTSTRAP_PASSWORD", "change-me-now"),
	}
	if cfg.SessionTTL <= 0 || cfg.WorkerLease <= 0 || cfg.WorkerBatchSize <= 0 {
		return Config{}, fmt.Errorf("durations and batch size must be positive")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
