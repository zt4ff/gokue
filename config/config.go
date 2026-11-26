// Package config provides configuration structures for the gokue job queue.
package config

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/zt4ff/gokue/v2/backend"
	"github.com/zt4ff/gokue/v2/internal/logger"
)

// Memory Type
type MemoryType string

const (
	// InMemory is the configuration to use in-memory backend.
	InMemory MemoryType = "in-memory"
	// MongoDB selects the MongoDB backend. It is not wired into the queue yet,
	// so Validate rejects it.
	MongoDB MemoryType = "mongo-db"
	// Persistent keeps jobs in Config.Store. gokue.WithStore selects it.
	Persistent MemoryType = "store"
)

// Backoff strategy
type BackoffStrategy string

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
	Constant BackoffStrategy = "constant"

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
	Linear BackoffStrategy = "linear"

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
	Exponential BackoffStrategy = "exponential"

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
	ExponentialJitter BackoffStrategy = "exponential-jitter"
)

// Config holds the configuration for a job queue.
type Config struct {
	// MemoryType is the storage type of the queue.
	MemoryType MemoryType
	// WorkerCount is the number of jobs executed simultaneously.
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
	BackoffStrategy BackoffStrategy
	// ShutdownTimeout is the maximum duration to wait for graceful shutdown.
	ShutdownTimeout time.Duration
	// RetryPredicates allows config-wide callback checks on when to retry failed jobs.
	//
	// If it returns `true`, the dispatcher will attempt to retry the job
	RetryPredicates func(error) bool
	// OnSuccess, OnFailure and OnRetry are lifecycle hooks called synchronously
	// on the worker goroutine. Each job outcome fires exactly one hook call:
	// OnRetry before each retry, then OnSuccess or OnFailure once. A panicking
	// hook is recovered. Nil hooks are skipped.
	OnSuccess func(JobEvent)
	OnFailure func(JobEvent)
	OnRetry   func(JobEvent)
	// Store, when set, makes the queue persistent: jobs are saved to Store on
	// submit and workers claim them from it. Codec is required with Store and
	// every submitted job type must be registered in it. QueueSize is ignored.
	// Use gokue.WithStore to set both.
	Store backend.JobStore
	Codec *backend.Codec
	// Lease is how long a claimed job may go without a heartbeat before
	// another worker may run it again. Only used with Store.
	Lease time.Duration
	// PollInterval is how often idle workers check Store for jobs saved by
	// other processes. Only used with Store.
	PollInterval time.Duration
	// Logger enables structured logging for the queue. When nil, logging is disabled.
	// Use gokue.WithLogger to set it.
	Logger logger.Logger
}

// ErrInvalidConfig is an error where config for a queue is invalid.
var ErrInvalidConfig = errors.New("invalid queue config")

// JobEvent describes a job outcome passed to lifecycle hooks.
type JobEvent struct {
	// Name is the job name.
	Name string
	// Attempt is the 1-based attempt that produced this outcome.
	Attempt int
	// Err is the error from the attempt; nil for OnSuccess.
	Err error
	// Duration is the time since the first attempt started.
	Duration time.Duration
	// RetryDelay is the delay before the next attempt; set only for OnRetry.
	RetryDelay time.Duration
}

// Default returns a Config with sensible default values.
func Default() Config {
	return Config{
		MemoryType:      InMemory,
		WorkerCount:     runtime.GOMAXPROCS(0),
		QueueSize:       1024,
		MaxRetries:      3,
		JobTimeout:      30 * time.Second,
		RetryDelay:      250 * time.Millisecond,
		MaxRetryDelay:   30 * time.Second,
		BackoffStrategy: Exponential,
		ShutdownTimeout: 10 * time.Second,
		Lease:           5 * time.Minute,
		PollInterval:    time.Second,
		RetryPredicates: nil,
		Logger:          nil,
	}
}

// Validate checks if the Config is valid and returns an error if any field is invalid.
func (c *Config) Validate() error {
	switch c.MemoryType {
	case InMemory:
		if c.Store != nil || c.Codec != nil {
			return fmt.Errorf("%w: store and codec require the %q backend", ErrInvalidConfig, Persistent)
		}
	case Persistent:
		if c.Store == nil || c.Codec == nil {
			return fmt.Errorf("%w: a store and a codec are both required", ErrInvalidConfig)
		}
		if c.Lease <= 0 {
			return fmt.Errorf("%w: lease must be greater than zero", ErrInvalidConfig)
		}
		if c.PollInterval <= 0 {
			return fmt.Errorf("%w: poll interval must be greater than zero", ErrInvalidConfig)
		}
	case MongoDB:
		return fmt.Errorf("%w: backend %q is not implemented yet", ErrInvalidConfig, c.MemoryType)
	default:
		return fmt.Errorf("%w: unsupported backend %q", ErrInvalidConfig, c.MemoryType)
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

	return nil
}
