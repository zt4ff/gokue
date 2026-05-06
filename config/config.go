package config

import (
	"errors"
	"fmt"
	"runtime"
	"time"
)

const (
	// InMemory is the configuration to use in-memory backend.
	InMemory = "in-memory"
	// MongoDB is the configuration to use Mongo DB as the backend.
	MongoDB = "mongo-db"
)

type Config struct {
	// Backend is the storage type of the queue.
	Backend string
	// WorkerCount is the CPU count simultaneosly executing the jobs.
	WorkerCount int
	// QueueSize is the maximum number of jobs that can be enqueued in a queue.
	QueueSize int
	// MaxRetries is the maximum number of retries a job can do if failed.
	MaxRetries int
	// JobTimeout is the maximum duration of time a job should run.
	JobTimeout time.Duration
	// RetryDelay is the amount of time between retries.
	RetryDelay      time.Duration
	ShutdownTimeout time.Duration
}

// ErrInvalidConfig is an error where config for a queue is invalid.
var ErrInvalidConfig = errors.New("invalid queue config")

// Default returns a Config with default values.
func Default() Config {
	return Config{
		Backend:         InMemory,
		WorkerCount:     runtime.GOMAXPROCS(0),
		QueueSize:       1024,
		MaxRetries:      3,
		JobTimeout:      30 * time.Second,
		RetryDelay:      250 * time.Millisecond,
		ShutdownTimeout: 10 * time.Second,
	}
}

// Validate checks if the Config
func (c *Config) Validate() error {
	switch c.Backend {
	case InMemory, MongoDB:
	default:
		return fmt.Errorf("%w: unsupported backend %q", ErrInvalidConfig, c.Backend)
	}

	if c.WorkerCount <= 0 {
		return fmt.Errorf("%w: worker count must be greater than zero", ErrInvalidConfig)
	}
	if c.QueueSize <= 0 {
		return fmt.Errorf("%w: queue size must be greater than zero", ErrInvalidConfig)
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("%w: max retries cannot be negative", ErrInvalidConfig)
	}

	return nil
}
