// Package config provides configuration structures for the gokue job queue.
package config

import (
	"errors"
	"fmt"
	"runtime"
	"time"
)

// Memory Type
const (
	// InMemory is the configuration to use in-memory backend.
	InMemory = "in-memory"
	// MongoDB is the configuration to use Mongo DB backend.
	// MongoDB = "mongo-db"
)

// Backoff strategy
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
	// RetryDelay is the base amount of time between retries.
	RetryDelay time.Duration
	// MaxRetryDelay is the maximum delay between retries (capped exponential backoff).
	// A zero or negative value means no cap.
	MaxRetryDelay time.Duration
	// BackoffStrategy is the strategy to handle resilience and how to calculate retry time.
	BackoffStrategy string
	// ShutdownTimeout is the maximum duration to wait for graceful shutdown.
	ShutdownTimeout time.Duration
	// RetryPredicates allows config-wide callback checks on when to retry failed jobs.
	//
	// If it returns `true`, the dispatcher will attempt to retry the job
	RetryPredicates func(error) bool
	// MaxJobStats is the maximum number of distinct job names tracked for
	// per-job statistics. Zero or negative means no limit. When the limit is
	// reached, additional job names are aggregated under the "untracked" bucket.
	// This only applies to the stats collector created internally.
	MaxJobStats int
	// LatencyHistogramBuckets sets the inclusive upper bounds (positive and
	// strictly increasing) of the per-job latency histogram buckets. Success
	// and failure latencies are histogrammed separately. Empty means no
	// latency histograms are collected. Only applies to the stats collector
	// created internally.
	LatencyHistogramBuckets []time.Duration
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
		MaxRetryDelay:   30 * time.Second,
		ShutdownTimeout: 10 * time.Second,
		BackoffStrategy: Exponential,
		RetryPredicates: nil,
		MaxJobStats:     10_000,
	}
}

// Validate checks if the Config is valid and returns an error if any field is invalid.
func (c *Config) Validate() error {
	switch c.Backend {
	case InMemory:
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

	if c.MaxRetryDelay < 0 {
		return fmt.Errorf("%w: max retry delay cannot be negative", ErrInvalidConfig)
	}

	if c.MaxJobStats < 0 {
		return fmt.Errorf("%w: max job stats cannot be negative", ErrInvalidConfig)
	}

	for i, b := range c.LatencyHistogramBuckets {
		if b <= 0 {
			return fmt.Errorf("%w: latency histogram buckets must be positive", ErrInvalidConfig)
		}
		if i > 0 && b <= c.LatencyHistogramBuckets[i-1] {
			return fmt.Errorf("%w: latency histogram buckets must be strictly increasing", ErrInvalidConfig)
		}
	}

	return nil
}
