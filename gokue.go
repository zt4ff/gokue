// Package gokue provides a job queue library for managing and executing tasks with configurable workers and retries.
package gokue

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/zt4ff/gokue/config"
	"github.com/zt4ff/gokue/dispatcher"
	"github.com/zt4ff/gokue/internal/logger"
	jobpkg "github.com/zt4ff/gokue/job"
	"github.com/zt4ff/gokue/stats"
)

// Job is a task that can be executed by the queue.
type Job = jobpkg.Job

// Option is a functional option for configuring a Queue.
type Option func(*config.Config)

// SubmitOption is a functional option for configuring a single job submission.
type SubmitOption func(*dispatcher.Task)

// MaxRetries returns a SubmitOption that overrides the global MaxRetries for this job.
func MaxRetries(maxRetries int) SubmitOption {
	return func(t *dispatcher.Task) {
		t.MaxRetries = &maxRetries
	}
}

// RetryDelay returns a SubmitOption that overrides the global RetryDelay for this job.
func RetryDelay(delay time.Duration) SubmitOption {
	return func(t *dispatcher.Task) {
		t.RetryDelay = &delay
	}
}

// Queue manages job submission, execution, and statistics using a pool of workers.
type Queue struct {
	// config holds the queue configuration.
	config config.Config
	// dispatcher handles job execution with worker pool.
	dispatcher *dispatcher.Dispatcher
	// collector tracks execution statistics.
	collector *stats.Collector
}

// WithMongoDB returns an Option that sets the Backend type to Mongo DB.
func WithMongoDB(cfg config.Config) Option {
	return func(target *config.Config) {
		target.Backend = config.MongoDB
	}
}

// WithWorkerCount returns an Option that sets the number of concurrent workers.
func WithWorkerCount(workerCount int) Option {
	return func(target *config.Config) {
		target.WorkerCount = workerCount
	}
}

// WithQueueSize returns an Option that sets the maximum queue size.
func WithQueueSize(queueSize int) Option {
	return func(target *config.Config) {
		target.QueueSize = queueSize
	}
}

// WithMaxRetries returns an Option that sets the maximum number of retries.
func WithMaxRetries(maxRetries int) Option {
	return func(target *config.Config) {
		target.MaxRetries = maxRetries
	}
}

// WithJobTimeout returns an Option that sets the job execution timeout.
func WithJobTimeout(timeout time.Duration) Option {
	return func(target *config.Config) {
		target.JobTimeout = timeout
	}
}

// WithRetryDelay returns an Option that sets the base delay between retries.
func WithRetryDelay(delay time.Duration) Option {
	return func(target *config.Config) {
		target.RetryDelay = delay
	}
}

// WithMaxRetryDelay returns an Option that sets the maximum delay between retries.
// Exponential backoff will not exceed this value. A zero or negative value means no cap.
func WithMaxRetryDelay(delay time.Duration) Option {
	return func(target *config.Config) {
		target.MaxRetryDelay = delay
	}
}

// WithShutdownTimeout returns an Option that sets the graceful shutdown timeout.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(target *config.Config) {
		target.ShutdownTimeout = timeout
	}
}

// WithLogger enables structured logging to w and returns the Option to pass to
// NewQueue plus a closer that flushes the logger and closes the write stream.
// Call the closer (typically via defer) when the queue is no longer needed.
// Logging is disabled by default.
func WithLogger(w io.Writer) (Option, func() error) {
	logger, closer := logger.NewLogger(w)
	return func(target *config.Config) {
		target.Logger = logger
	}, closer
}

// WithBackoffStrategy defines the backoff strategy to employ when failure happens. The strategies includes:
//
// Constant is default. It is the backoff strategy that retries after a fixed delay.
//
// Formula:
//
//	delay = B
//
// Example:
//
//	B = 2s
//	retries = 2s, 2s, 2s, 2s...
//
// B = base retry delay.
// Linear is the backoff strategy that increases the retry delay
// by a fixed amount on every retry.
//
// Formula:
//
//	delay = B * N
//
// Example:
//
//	B = 2s
//	retries = 2s, 4s, 6s, 8s...
//
// B = base retry delay.
// N = retry attempt number starting from 1.
//
// Exponential is the backoff strategy that doubles the retry delay
// on every retry attempt.
//
// Formula:
//
//	delay = B * 2^(N-1)
//
// Example:
//
//	B = 2s
//	retries = 2s, 4s, 8s, 16s...
//
// B = base retry delay.
// N = retry attempt number starting from 1.
//
// ExponentialJitter is the exponential backoff strategy with
// randomness added to reduce synchronized retries and retry storms.
//
// Formula:
//
//	delay = random(0, B * 2^(N-1))
//
// Example:
//
//	B = 2s
//	retries ≈ 1.2s, 3.8s, 5.1s, 14.7s...
//
// B = base retry delay.
// N = retry attempt number starting from 1.
func WithBackoffStrategy(stragegy string) Option {
	return func(target *config.Config) {
		target.BackoffStrategy = stragegy
	}
}

// NewQueue creates and returns a new Queue with the provided configuration options.
// It returns an error if the configuration is invalid.
// Logging is disabled by default; pass WithLogger to enable it.
func NewQueue(options ...Option) (*Queue, error) {
	return NewQueueWithLogger(nil, options...)
}

// NewQueueWithLogger creates and returns a new Queue with the provided configuration options and logger.
// It returns an error if the configuration is invalid.
// If logger is nil, logging is disabled.
func NewQueueWithLogger(logger logger.Logger, options ...Option) (*Queue, error) {
	queueConfig := config.Default()
	for _, option := range options {
		if option != nil {
			option(&queueConfig)
		}
	}
	if logger != nil {
		queueConfig.Logger = logger
	}

	if err := queueConfig.Validate(); err != nil {
		return nil, err
	}

	collector := stats.NewCollector()
	queue := &Queue{
		config:    queueConfig,
		collector: collector,
	}
	queue.dispatcher = dispatcher.NewDispatcher(queueConfig, collector)

	return queue, nil
}

// Submit adds a job to the queue, blocking until the job is enqueued or the context is cancelled.
// The job's name is derived from its type, or from its Name method if it implements job.NamedJob.
// SubmitOption arguments allow per-job overrides for MaxRetries and RetryDelay.
func (q *Queue) Submit(ctx context.Context, task Job, opts ...SubmitOption) error {
	if q == nil {
		return errors.New("queue is nil")
	}
	if task == nil {
		return errors.New("job cannot be nil")
	}
	if ctx == nil {
		return errors.New("context cannot be nil")
	}

	t := dispatcher.Task{Name: jobpkg.NameOf(task), Job: task}
	for _, opt := range opts {
		if opt != nil {
			opt(&t)
		}
	}
	return q.dispatcher.Submit(ctx, t)
}

// TrySubmit attempts to add a job to the queue without blocking.
// The job's name is derived from its type, or from its Name method if it implements job.NamedJob.
// SubmitOption arguments allow per-job overrides for MaxRetries and RetryDelay.
func (q *Queue) TrySubmit(ctx context.Context, task Job, opts ...SubmitOption) error {
	if q == nil {
		return errors.New("queue is nil")
	}
	if task == nil {
		return errors.New("job cannot be nil")
	}
	if ctx == nil {
		return errors.New("context cannot be nil")
	}

	t := dispatcher.Task{Name: jobpkg.NameOf(task), Job: task}
	for _, opt := range opts {
		if opt != nil {
			opt(&t)
		}
	}
	return q.dispatcher.TrySubmit(ctx, t)
}

// Run submits a job to the queue using a background context.
// The job's name is derived from its type, or from its Name method if it implements job.NamedJob.
// SubmitOption arguments allow per-job overrides for MaxRetries and RetryDelay.
func (q *Queue) Run(task Job, opts ...SubmitOption) {
	if q == nil {
		return
	}
	_ = q.Submit(context.Background(), task, opts...)
}

// Close gracefully shuts down the queue and waits for all workers to finish processing.
func (q *Queue) Close(ctx context.Context) error {
	if q == nil {
		return errors.New("queue is nil")
	}
	if ctx == nil {
		return errors.New("context cannot be nil")
	}
	return q.dispatcher.Close(ctx)
}

// Stats returns a snapshot of the current execution statistics.
func (q *Queue) Stats() stats.Snapshot {
	if q == nil || q.collector == nil {
		return stats.Snapshot{}
	}
	return q.collector.Snapshot()
}
