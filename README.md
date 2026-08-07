# gokue

[![CI](https://github.com/zt4ff/gokue/actions/workflows/ci.yml/badge.svg)](https://github.com/zt4ff/gokue/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/zt4ff/gokue/branch/main/graph/badge.svg)](https://codecov.io/gh/zt4ff/gokue)

`gokue` is a bounded in-memory job queue with worker pools, per-job timeouts, retries, and atomic runtime stats.

## Features

- Bounded queue with backpressure
- Worker pool sized by configuration
- Job timeouts and retry delays
- Panic recovery per job execution
- Atomic queue metrics
- Structured logging via zap (opt-in)

## Basic Usage

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/zt4ff/gokue"
)

// EmailJob implements the gokue.Job interface. Process returns nil on
// success and an error to trigger a retry (up to MaxRetries).
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

## Submit Methods

- `Submit` blocks until the job is accepted or the context is canceled.
- `TrySubmit` returns immediately with `dispatcher.ErrQueueFull` when the queue has no capacity.
- `Run` submits with a background context.

## Job Names

Job names are derived from the job's type, so no registration or string identifiers are needed. To use a stable custom name (e.g. to keep identity across type renames), implement the optional `Name()` method:

```go
type EmailJob struct{ ... }

func (j EmailJob) Process(ctx context.Context) error { ... }

func (j EmailJob) Name() string { return "send email" }
```

Jobs that don't implement `Name()` are identified by their type name; anonymous types resolve to `"anonymous"`.

## Logging

Logging is disabled by default. To enable it, pass `WithLogger` to `NewQueue` with an `io.Writer` to write JSON logs to; it returns an option plus a closer that flushes and closes the write stream:

```go
logOpt, stopLogging := gokue.WithLogger(os.Stdout)
defer stopLogging()

q, err := gokue.NewQueue(logOpt)
```

Omit `WithLogger` to keep the queue silent.

## Shutdown

Call `Close` with a context to wait for workers to drain in-flight jobs. Use a timeout context for bounded shutdown in production.
