// Package config provides configuration structures for the gokue job queue.
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
	// MongoDB is the configuration to use Mongo DB backend.
	MongoDB = "mongo-db"
)

const (
	// Constant is the backoff strategy that retries after a fixed delay.
	//
	// Formula:
	//   delay = B
	//
	// Example:
	//   B = 2s
	//   retries = 2s, 2s, 2s, 2s...
	//
	// B = base retry delay.
	Constant = "constant"

	// Linear is the backoff strategy that increases the retry delay
	// by a fixed amount on every retry.
	//
	// Formula:
	//   delay = B * N
	//
	// Example:
	//   B = 2s
	//   retries = 2s, 4s, 6s, 8s...
	//
	// B = base retry delay.
	// N = retry attempt number starting from 1.
	Linear = "linear"

	// Exponential is the backoff strategy that doubles the retry delay
	// on every retry attempt.
	//
	// Formula:
	//   delay = B * 2^(N-1)
	//
	// Example:
	//   B = 2s
	//   retries = 2s, 4s, 8s, 16s...
	//
	// B = base retry delay.
	// N = retry attempt number starting from 1.
	Exponential = "exponential"

	// ExponentialJitter is the exponential backoff strategy with
	// randomness added to reduce synchronized retries and retry storms.
	//
	// Formula:
	//   delay = random(0, B * 2^(N-1))
	//
	// Example:
	//   B = 2s
	//   retries ≈ 1.2s, 3.8s, 5.1s, 14.7s...
	//
	// B = base retry delay.
	// N = retry attempt number starting from 1.
	ExponentialJitter = "exponential-jitter"
)

// Config holds the configuration for a job queue.
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
	RetryDelay time.Duration
	// BackoffStrategy is the strategy to handle resilience and how to calculate retry time.
	BackoffStrategy string
	// ShutdownTimeout is the maximum duration to wait for graceful shutdown.
	ShutdownTimeout time.Duration
}

// ErrInvalidConfig is an error where config for a queue is invalid.
var ErrInvalidConfig = errors.New("invalid queue config")

// Default returns a Config with sensible default values.
func Default() Config {
	return Config{
		Backend:         InMemory,
		WorkerCount:     runtime.GOMAXPROCS(0),
		QueueSize:       1024,
		MaxRetries:      3,
		JobTimeout:      30 * time.Second,
		RetryDelay:      250 * time.Millisecond,
		ShutdownTimeout: 10 * time.Second,
		BackoffStrategy: Exponential,
	}
}

// Validate checks if the Config is valid and returns an error if any field is invalid.
func (c *Config) Validate() error {
	switch c.Backend {
	case InMemory, MongoDB:
	default:
		return fmt.Errorf("%w: unsupported backend %q", ErrInvalidConfig, c.Backend)
	}

	switch c.BackoffStrategy {
	case Constant, Linear, Exponential, ExponentialJitter:
	default:
		return fmt.Errorf("%w: unsupported backoff strategy %q", ErrInvalidConfig, c.BackoffStrategy)
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
	if c.JobTimeout < 0 {
		return fmt.Errorf("%w: job timeout cannot be negative", ErrInvalidConfig)
	}
	if c.ShutdownTimeout < 0 {
		return fmt.Errorf("%w: shutdown timeout cannot be negative", ErrInvalidConfig)
	}
	if c.RetryDelay < 0 {
		return fmt.Errorf("%w: retry delay cannot be negative", ErrInvalidConfig)
	}

	return nil
}
