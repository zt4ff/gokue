# gokue

[![CI](https://github.com/zt4ff/gokue/actions/workflows/ci.yml/badge.svg)](https://github.com/zt4ff/gokue/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/zt4ff/gokue/branch/main/graph/badge.svg)](https://codecov.io/gh/zt4ff/gokue)

`gokue` is a bounded, **in-memory** job queue for Go. It processes jobs with a
configurable worker pool and ships with per-job timeouts, retries with pluggable
backoff, panic recovery, and atomic runtime statistics.

## Features

- **Bounded queue with backpressure** - submissions block when the queue is full.
- **Worker pool** - concurrent workers sized by configuration.
- **Retries with backoff** - constant, linear, exponential, or jittered delays.
- **Conditional retries** - retry predicates decide which errors are worth retrying.
- **Per-job timeouts** - each attempt gets a cancellable context.
- **Per-job overrides** - max retries and retry delay can be set per submission.
- **Panic recovery** - a panicking job is treated as a failure, not a crash.
- **Atomic runtime stats** - lock-free counters for enqueued, processed, failed, retried, and dropped.
- **Structured logging** - JSON logs via zap (opt-in).

## Installation


```sh
go get github.com/zt4ff/gokue
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/zt4ff/gokue"
)

// EmailJob implements the gokue.Job interface. Return nil on success, or an
// error to trigger a retry (up to the configured MaxRetries).
type EmailJob struct {
	To   string
	Body string
}

func (j EmailJob) Process(ctx context.Context) error {
	// Check for cancellation/timeout before doing work.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	fmt.Printf("sending email to %s\n", j.To)
	return nil
}

func main() {
	// Create a queue with 4 workers and retries.
	q, err := gokue.NewQueue(
		gokue.WithWorkerCount(4),
		gokue.WithQueueSize(1024),
		gokue.WithMaxRetries(3),
		gokue.WithJobTimeout(30*time.Second),
		gokue.WithRetryDelay(250*time.Millisecond),
	)
	if err != nil {
		panic(err)
	}

	// Submit a job. The job's name is derived from its type (EmailJob).
	// This blocks until the job is enqueued or ctx is done.
	err = q.Submit(context.Background(), EmailJob{
		To:   "user@example.com",
		Body: "Hello from gokue",
	})
	if err != nil {
		panic(err)
	}

	// Inspect runtime stats.
	stats := q.Stats()
	fmt.Printf("enqueued: %d, processed: %d, failed: %d\n",
		stats.Enqueued, stats.Processed, stats.Failed)

	// Gracefully shut down: drain in-flight jobs or time out after 5s.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := q.Close(shutdownCtx); err != nil {
		panic(err)
	}
}
```

## Core Concepts

### Jobs

A `Job` is anything that implements the `Process` method:

```go
type Job interface {
	Process(context.Context) error
}
```

- Return `nil` to signal success.
- Return an error to trigger a retry. `MaxRetries` is the number of *retries
  after the initial attempt*: with `MaxRetries(3)` a failing job runs up to
  4 times total (1 initial + 3 retries). `MaxRetries(0)` runs the job exactly once.
- The `context.Context` passed to `Process` is cancelled when the job's
  configured timeout elapses, so long-running jobs should check `ctx.Done()`.

Panics inside `Process` are recovered and converted into an error, so they
follow the normal retry path and count toward `MaxRetries` like any other
failure.

### Queues

`NewQueue(options ...Option)` creates a queue and starts its worker pool. It
returns an error if the configuration is invalid (see [Configuration](#configuration)).
Logging is disabled by default; pass `WithLogger` to enable it.

## Configuration

`gokue` uses functional options. Every setting has a sensible default, so
`gokue.NewQueue()` with no options already works.

| Option | Default | Description |
| --- | --- | --- |
| `WithWorkerCount(n)` | `GOMAXPROCS` | Number of concurrent worker goroutines. Must be > 0. |
| `WithQueueSize(n)` | `1024` | Maximum number of queued (unprocessed) jobs. Must be > 0. |
| `WithMaxRetries(n)` | `3` | Number of retries *after* the initial attempt (`0` = run once, no retries). |
| `WithJobTimeout(d)` | `30s` | Per-attempt timeout. A job running longer is cancelled via context. |
| `WithRetryDelay(d)` | `250ms` | Base delay between retries. |
| `WithMaxRetryDelay(d)` | `30s` | Ceiling for exponential/jittered backoff. `<= 0` means no cap. |
| `WithShutdownTimeout(d)` | `10s` | Validated but not yet applied; `Close` is bounded by the context you pass it. |
| `WithLogger(w)` | disabled | Enable structured JSON logging to `w` (see [Logging](#logging)). |

Options are applied in the order you pass them.

### Backoff strategies

Retry delays are calculated from the base `RetryDelay` using the configured
strategy. Choose one via `gokue.WithBackoffStrategy`.

| Strategy | Delay formula | Example (B = 2s) |
| --- | --- | --- |
| `constant` | `B` | 2s, 2s, 2s, 2s |
| `linear` | `B * N` | 2s, 4s, 6s, 8s |
| `exponential` (default) | `B * 2^(N-1)`, capped at `MaxRetryDelay` | 2s, 4s, 8s, 16s |
| `exponential-jitter` | `random(0, B * 2^(N-1))`, capped | ~1.2s, ~3.8s, ~5.1s, ~14.7s |

`N` is the retry attempt number starting from 1. Only the exponential
strategies are capped by `MaxRetryDelay`; `constant` and `linear` use
`RetryDelay` as-is.

### Retry predicates

By default, gokue retries any error. A retry predicate lets you decide *which*
errors deserve a retry - for example, never retry validation errors:

```go
import "github.com/zt4ff/gokue/config"

var errPermanent = errors.New("permanent failure")

cfg := config.Default()
cfg.RetryPredicates = func(err error) bool {
	// true -> retry, false -> fail immediately
	return !errors.Is(err, errPermanent)
}

q, err := gokue.NewQueue(
	gokue.WithBackOffStrategy(config.InMemory)
	gokue.WithWorkerCount(1),
	gokue.WithQueueSize(64),
	gokue.WithMaxRetries(2),
	gokue.WithJobTimeout(5*time.Second),
)
```

When the predicate returns `false`, the job fails immediately and the
`RetryPredicatesFailed` counter is incremented.

## Submitting Jobs

| Method | Behavior |
| --- | --- |
| `Submit(ctx, job, opts...)` | Blocks until the job is enqueued or `ctx` is cancelled. |
| `TrySubmit(ctx, job, opts...)` | Returns immediately with `dispatcher.ErrQueueFull` when the queue is full. |
| `Run(job, opts...)` | Submits with `context.Background()`, discarding the error. |

All three accept per-submission options to override the queue-level settings.
`MaxRetries` and `RetryDelay` are the only per-submission overrides (there are
no per-submission overrides for timeouts or backoff strategy):

```go
q.Submit(ctx, job, gokue.MaxRetries(0))                 // disable retries for this job
q.Submit(ctx, job, gokue.RetryDelay(5*time.Second))     // longer delay for this job
```

### Job names

Job names are derived from the job's type, so no registration or string
identifiers are needed. To use a stable custom name (e.g. to keep identity
across type renames), implement the optional `Name()` method:

```go
type EmailJob struct{ ... }

func (j EmailJob) Process(ctx context.Context) error { ... }

func (j EmailJob) Name() string { return "send email" }
```

Jobs that don't implement `Name()` are identified by their type name; anonymous
types resolve to `"anonymous"`.

### Errors

- `NewQueue` returns an error wrapping `config.ErrInvalidConfig` for invalid settings.
- `Submit` / `TrySubmit` return `dispatcher.ErrClosed` after the queue is closed.
- `TrySubmit` returns `dispatcher.ErrQueueFull` when the queue has no capacity.
- `Submit` returns `ctx.Err()` if the context is cancelled while waiting to enqueue.

Import `github.com/zt4ff/gokue/dispatcher` to compare against its sentinel
errors with `errors.Is`:

```go
import (
	"errors"

	"github.com/zt4ff/gokue/dispatcher"
)

err := q.TrySubmit(ctx, job)
switch {
case errors.Is(err, dispatcher.ErrQueueFull):
	// queue at capacity; retry later
case errors.Is(err, dispatcher.ErrClosed):
	// queue is shut down
}
```

## Statistics

`Stats()` returns a point-in-time snapshot with atomic counters:

```go
stats := q.Stats()
fmt.Println(stats.Enqueued)              // jobs accepted into the queue
fmt.Println(stats.Processed)             // jobs completed successfully
fmt.Println(stats.Failed)                // jobs failed after all retries
fmt.Println(stats.Retried)               // retry attempts (a job retried 3x counts 3)
fmt.Println(stats.Dropped)               // jobs dropped (TrySubmit at capacity, or Submit ctx cancelled)
fmt.Println(stats.RetryPredicatesFailed) // jobs failed by a retry predicate
```

## Logging

Logging is disabled by default. Enable it with `WithLogger`, which returns an
option plus a closer that flushes the logger and closes the write stream:

```go
logOpt, stopLogging := gokue.WithLogger(os.Stdout)
defer stopLogging()

q, err := gokue.NewQueue(logOpt)
```

Logs are written as JSON lines covering queue lifecycle events, submissions,
processing attempts, retries, successes, failures, panics, and shutdown.

## Graceful Shutdown

Call `Close(ctx)` to stop accepting new submissions and wait for workers to
drain in-flight and queued jobs:

- New submissions are rejected with `dispatcher.ErrClosed`.
- Retry sleeps are interrupted so shutdown isn't blocked by backoff delays.
- Returns `nil` when all workers finish before `ctx` is cancelled.
- Returns `ctx.Err()` if the context expires first - workers continue running in
  the background, so don't tear down shared resources immediately after.

Use a timeout context for bounded shutdown in production:

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
if err := q.Close(ctx); err != nil {
	log.Printf("shutdown timed out: %v", err)
}
```

## Roadmap

See [ROADMAP.md](ROADMAP.md) for the development plan (persistent backends, DLQ,
scheduling, priorities, and more).
