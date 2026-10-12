# gokue

<p align="center">
  <img src="./gokue.png" alt="Gokue Logo" width="180" />
</p>

`gokue` is a small, dependency-light job queue for Go. It runs jobs on a fixed
worker pool with a bounded in-memory queue, and provides per-attempt timeouts,
retries with configurable backoff, panic recovery, atomic statistics, and
optional structured logging.

[![Go Reference](https://pkg.go.dev/badge/github.com/zt4ff/gokue.svg)](https://pkg.go.dev/github.com/zt4ff/gokue)
[![CI](https://github.com/zt4ff/gokue/actions/workflows/ci.yml/badge.svg)](https://github.com/zt4ff/gokue/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/zt4ff/gokue)](https://github.com/zt4ff/gokue/blob/main/go.mod)
[![codecov](https://codecov.io/gh/zt4ff/gokue/branch/main/graph/badge.svg)](https://codecov.io/gh/zt4ff/gokue)

## Overview

`gokue` is an in-process job queue. Jobs are plain Go values that implement a
single `Process` method; the queue schedules them across a bound pool of worker
goroutines and applies retry, timeout, and panic-handling policy. It is intended
for background work within a single Go service — sending email, resizing images,
calling an API, and similar best-effort tasks — where adding an external broker
would be unnecessary weight.

The queue is **in-memory only**. Jobs do not survive a process restart, and no
external service is required to use the core package.

## Features

- Bounded in-memory queue with a configurable capacity.
- Fixed pool of worker goroutines.
- Blocking (`Submit`), non-blocking (`TrySubmit`), and fire-and-forget (`Run`) submission.
- Retries with `Constant`, `Linear`, `Exponential`, and `ExponentialJitter` backoff.
- Per-attempt job timeout and per-submission retry overrides.
- Config-level retry predicate to decide whether a given error is retryable.
- Job panic recovery; a panicking job is treated as a failure.
- Atomic, lock-free statistics snapshot via `Stats`.
- Optional structured JSON logging via `WithLogger`.
- Graceful shutdown that drains running and queued jobs (plus an immediate mode on `dispatcher`).

A standalone persistence layer also exists (`backend` and `backend/mongodb`). It
is **not wired into the queue yet**; see [Status](#status).

## Installation

```
go get github.com/zt4ff/gokue@latest
```

Requires Go **1.22.2** or newer (the version declared in `go.mod`).

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/zt4ff/gokue"
)

// EmailJob is a data-only job.
type EmailJob struct {
	To      string
	Message string
}

// Process implements gokue.Job.
func (j EmailJob) Process(ctx context.Context) error {
	select {
	case <-time.After(100 * time.Millisecond):
		fmt.Printf("sent %q to %s\n", j.Message, j.To)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func main() {
	q, err := gokue.NewQueue(
		gokue.WithWorkerCount(2),
		gokue.WithQueueSize(64),
	)
	if err != nil {
		log.Fatal(err)
	}

	if err := q.Submit(context.Background(), EmailJob{To: "user@example.com", Message: "hello"}); err != nil {
		log.Fatal(err)
	}

	// Close drains running and queued jobs before returning.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := q.Close(ctx); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("stats: %+v\n", q.Stats())
}
```

```text
sent "hello" to user@example.com
stats: {Enqueued:1 Processed:1 Failed:0 Retried:0 Dropped:0 RetryPredicatesFailed:0}
```

## Core concepts

### Job

```go
// gokue.Job is an alias for job.Job.
type Job = job.Job

type Job interface {
	Process(ctx context.Context) error
}
```

Any concrete type with a `Process(context.Context) error` method satisfies the
interface. A method declared on a value receiver means both the value and a
pointer satisfy `Job`; a method declared on a pointer receiver means only the
pointer does.

### NamedJob and job names

```go
type NamedJob interface {
	Job
	Name() string
}
```

`job.NameOf` resolves the name used in logs and errors: an explicit non-empty
`Name()` when the job implements `NamedJob`, otherwise the type name (pointer
receivers are dereferenced). Nil jobs and anonymous types resolve to
`"anonymous"`. Names are only used for observability; they are not required to be
unique.

### Queue and dispatcher

`gokue.Queue` is the public entry point. It owns a `dispatcher.Dispatcher`, which
runs the worker pool and the retry loop. Most programs use `Queue`; the
`dispatcher` package is exported for advanced use (custom task types, or
`CloseWithMode` for immediate shutdown).

```go
func NewQueue(options ...Option) (*Queue, error)
func (q *Queue) Submit(ctx context.Context, task Job, opts ...SubmitOption) error
func (q *Queue) TrySubmit(ctx context.Context, task Job, opts ...SubmitOption) error
func (q *Queue) Run(task Job, opts ...SubmitOption)
func (q *Queue) Close(ctx context.Context) error
func (q *Queue) Stats() stats.Snapshot
```

### Configuration

`config.Config` holds all queue settings. `config.Default()` supplies the
defaults, and `gokue.Option` functions override individual fields.

```go
type Option func(*config.Config)
```

Because `Option` is an ordinary function of the exported `Config`, any `Config`
field can be set directly with a custom option, including fields that have no
dedicated helper (for example `RetryPredicates`).

### Backends

`backend` defines a storage contract for jobs, independent of the queue:

```go
type JobStore interface {
	Save(ctx context.Context, rec JobRecord) error
	Get(ctx context.Context, id job.ID) (JobRecord, error)
	Claim(ctx context.Context) (JobRecord, error)
	Update(ctx context.Context, rec JobRecord) error
	Delete(ctx context.Context, id job.ID) error
	List(ctx context.Context, f ListFilter) ([]JobRecord, error)
	RequeueStale(ctx context.Context, lease time.Duration) (int, error)
	Close(ctx context.Context) error
}
```

`backend.MemoryStore` implements it in memory, `backend.Codec` serializes jobs to
and from JSON payloads, and `backend/mongodb` provides a MongoDB-backed store.
These types are usable today but are **not** connected to `gokue.Queue`; see
[Status](#status).

## Usage

### Defining a job

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zt4ff/gokue"
)

// Value receiver: both EmailJob and *EmailJob satisfy gokue.Job.
type EmailJob struct {
	To   string
	Body string
}

func (j EmailJob) Process(ctx context.Context) error {
	fmt.Printf("email -> %s\n", j.To)
	return nil
}

// Pointer receiver: only *UploadJob satisfies gokue.Job.
type UploadJob struct {
	Path string
}

func (j *UploadJob) Process(ctx context.Context) error {
	fmt.Printf("upload %s\n", j.Path)
	return nil
}

// Explicit name: reported as "daily-report" instead of "ReportJob".
type ReportJob struct{}

func (ReportJob) Process(ctx context.Context) error { return nil }
func (ReportJob) Name() string                      { return "daily-report" }

func main() {
	q, err := gokue.NewQueue()
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := q.Close(context.Background()); err != nil {
			log.Fatal(err)
		}
	}()

	ctx := context.Background()
	if err := q.Submit(ctx, EmailJob{To: "a@example.com", Body: "hi"}); err != nil {
		log.Fatal(err)
	}
	if err := q.Submit(ctx, &UploadJob{Path: "/tmp/file"}); err != nil {
		log.Fatal(err)
	}
	if err := q.Submit(ctx, ReportJob{}); err != nil {
		log.Fatal(err)
	}
}
```

### Configuring the queue

```go
q, err := gokue.NewQueue(
	gokue.WithWorkerCount(4),
	gokue.WithQueueSize(256),
	gokue.WithMaxRetries(5),
	gokue.WithJobTimeout(30*time.Second),
	gokue.WithRetryDelay(250*time.Millisecond),
	gokue.WithMaxRetryDelay(10*time.Second),
	gokue.WithBackoffStrategy(config.Exponential),
)
if err != nil {
	log.Fatal(err)
}
```

### Submitting jobs and handling a full queue

`Submit` blocks until the job is enqueued or the context is done. `TrySubmit`
never blocks and returns `dispatcher.ErrQueueFull` when the queue is at capacity.
When the queue is full, the `Dropped` statistic is incremented for rejected
attempts.

```go
release := make(chan struct{})

q, err := gokue.NewQueue(gokue.WithWorkerCount(1), gokue.WithQueueSize(1))
if err != nil {
	log.Fatal(err)
}

ctx := context.Background()

// Occupy the single worker and fill the single queue slot.
if err := q.Submit(ctx, &BlockingJob{release: release}); err != nil {
	log.Fatal(err)
}
if err := q.Submit(ctx, &BlockingJob{release: release}); err != nil {
	log.Fatal(err)
}

// TrySubmit returns immediately instead of blocking.
err = q.TrySubmit(ctx, &BlockingJob{release: release})
if errors.Is(err, dispatcher.ErrQueueFull) {
	fmt.Println("queue full, job not enqueued")
} else if err != nil {
	log.Fatal(err)
}

// Submit blocks until a slot is free or the context is done.
waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
defer cancel()
if err := q.Submit(waitCtx, &BlockingJob{release: release}); err != nil {
	fmt.Println("submit gave up:", err)
}

close(release)
```

```text
queue full, job not enqueued
submit gave up: context deadline exceeded
```

### Context cancellation and timeouts

The context passed to `Submit`/`TrySubmit` bounds only the time spent waiting for
queue capacity. Each execution attempt instead receives its own context,
cancelled when the configured `JobTimeout` elapses. Jobs should honor
`ctx.Done()`.

```go
q, err := gokue.NewQueue(
	gokue.WithWorkerCount(1),
	gokue.WithJobTimeout(500*time.Millisecond),
	gokue.WithMaxRetries(0),
)
if err != nil {
	log.Fatal(err)
}

if err := q.Submit(context.Background(), SlowJob{}); err != nil {
	log.Fatal(err)
}
```

A `SlowJob` that waits on `ctx.Done()` prints, for example:

```text
slow cancelled: context deadline exceeded
```

### Retries and backoff

A job is attempted once plus up to `MaxRetries` additional times. The delay
between attempts depends on `BackoffStrategy` and `RetryDelay`, optionally capped
by `MaxRetryDelay`. `MaxRetries` and `RetryDelay` can be overridden per
submission.

```go
q, err := gokue.NewQueue(
	gokue.WithMaxRetries(5),
	gokue.WithRetryDelay(250*time.Millisecond),
	gokue.WithMaxRetryDelay(5*time.Second),
	gokue.WithBackoffStrategy(config.Exponential),
)
if err != nil {
	log.Fatal(err)
}

ctx := context.Background()

// Queue-level retry settings.
if err := q.Submit(ctx, FlakyJob{}); err != nil {
	log.Fatal(err)
}

// Per-submission overrides.
if err := q.Submit(ctx, FlakyJob{}, gokue.MaxRetries(1), gokue.RetryDelay(10*time.Millisecond)); err != nil {
	log.Fatal(err)
}
```

To stop retrying for specific errors, set a retry predicate with a custom option:

```go
q, err := gokue.NewQueue(func(c *config.Config) {
	c.RetryPredicates = func(err error) bool {
		return errors.Is(err, errTemporary)
	}
})
if err != nil {
	log.Fatal(err)
}
```

When the predicate returns `false`, the job is marked failed immediately and the
`RetryPredicatesFailed` statistic is incremented.

### Graceful shutdown

`Close` rejects new submissions and waits for running and queued jobs to finish,
including retries and their backoff delays. It returns the context error if the
deadline is reached first.

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := q.Close(ctx); err != nil {
	if errors.Is(err, context.DeadlineExceeded) {
		log.Println("shutdown timed out; some jobs were still running")
	} else {
		log.Fatal(err)
	}
}
```

For an immediate return that does not wait for workers, the dispatcher exposes:

```go
d.CloseWithMode(context.Background(), dispatcher.ImmediateMode)
```

### Error handling

Errors from the queue are compared with `errors.Is`.

```go
switch {
case errors.Is(err, dispatcher.ErrClosed):
	// The queue has been closed.
case errors.Is(err, dispatcher.ErrQueueFull):
	// TrySubmit found the queue at capacity.
case errors.Is(err, context.Canceled):
	// The submit context was cancelled while waiting.
case errors.Is(err, context.DeadlineExceeded):
	// The submit context or the shutdown context timed out.
}
```

### Logging

Logging is disabled by default. `WithLogger` returns an option plus a closer that
flushes and closes the writer; call the closer when the queue is no longer
needed.

```go
opt, closer := gokue.WithLogger(os.Stdout)
defer func() {
	if err := closer(); err != nil {
		log.Fatal(err)
	}
}()

q, err := gokue.NewQueue(opt, gokue.WithWorkerCount(1))
if err != nil {
	log.Fatal(err)
}
```

Log lines are JSON with the zap production encoder, for example:

```json
{"level":"info","ts":1791817682.6266255,"msg":"dispatcher_close_started","mode":"drain"}
```

### Persistence layer (standalone)

`backend.Codec` serializes jobs to JSON, and a `backend.JobStore` stores the
resulting payload with bookkeeping (status, attempts, version, timestamps). This
layer is independent of the queue.

```go
codec := backend.NewCodec()
if err := codec.Register("", EmailJob{}); err != nil {
	log.Fatal(err)
}

name, payload, err := codec.Encode(EmailJob{To: "a@example.com", Body: "hi"})
if err != nil {
	log.Fatal(err)
}

store := backend.NewMemoryStore()
defer func() {
	if err := store.Close(context.Background()); err != nil {
		log.Fatal(err)
	}
}()

if err := store.Save(context.Background(), backend.JobRecord{
	ID:        job.ID("job-1"),
	Name:      name,
	Payload:   payload,
	CreatedAt: time.Now().UTC(),
}); err != nil {
	log.Fatal(err)
}

claimed, err := store.Claim(context.Background())
if err != nil {
	log.Fatal(err)
}
decoded, err := codec.Decode(claimed.Name, claimed.Payload)
if err != nil {
	log.Fatal(err)
}
```

`backend.Codec` uses JSON, so only exported fields are persisted;
`Register` rejects types that would not round-trip (for example, types with no
exported fields) with `backend.ErrNoSerializableFields`.

## Configuration reference

### Queue options

| Option | Argument | Default | Description |
| --- | --- | --- | --- |
| `WithWorkerCount` | `int` | `runtime.GOMAXPROCS(0)` | Number of worker goroutines. Must be `> 0`. |
| `WithQueueSize` | `int` | `1024` | Queue capacity. Must be `> 0`. |
| `WithMaxRetries` | `int` | `3` | Retries after the first attempt. Must be `>= 0`. |
| `WithJobTimeout` | `time.Duration` | `30s` | Per-attempt timeout. `0` disables it. Must be `>= 0`. |
| `WithRetryDelay` | `time.Duration` | `250ms` | Base delay between retries. Must be `>= 0`. |
| `WithMaxRetryDelay` | `time.Duration` | `30s` | Cap for exponential backoff. `<= 0` means no cap. |
| `WithBackoffStrategy` | `config.BackoffStrategy` | `Exponential` | One of `Constant`, `Linear`, `Exponential`, `ExponentialJitter`. |
| `WithLogger` | `io.Writer` | disabled | Enables structured JSON logging; also returns a closer. |
| `WithShutdownTimeout` | `time.Duration` | `10s` | Sets `Config.ShutdownTimeout`. Validated but not currently consumed by `Close`. |
| `WithMongoDB` | `config.Config` | — | Sets `Config.MemoryType` to `mongo-db`. Has no effect on the running queue. |

### Per-submission options

| Option | Argument | Description |
| --- | --- | --- |
| `MaxRetries` | `int` | Overrides the queue-level `MaxRetries` for this job. |
| `RetryDelay` | `time.Duration` | Overrides the queue-level `RetryDelay` for this job. |

### `config.Config` fields

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `MemoryType` | `config.MemoryType` | `config.InMemory` | `InMemory` or `MongoDB`; only `InMemory` is implemented. |
| `WorkerCount` | `int` | `GOMAXPROCS(0)` | Must be `> 0`. |
| `QueueSize` | `int` | `1024` | Must be `> 0`. |
| `MaxRetries` | `int` | `3` | Must be `>= 0`. |
| `JobTimeout` | `time.Duration` | `30s` | Must be `>= 0`. |
| `RetryDelay` | `time.Duration` | `250ms` | Must be `>= 0`. |
| `MaxRetryDelay` | `time.Duration` | `30s` | Must be `>= 0`. |
| `BackoffStrategy` | `config.BackoffStrategy` | `Exponential` | Must be a known strategy. |
| `ShutdownTimeout` | `time.Duration` | `10s` | Validated but not currently used. |
| `RetryPredicates` | `func(error) bool` | `nil` | Set via a custom option; `true` retries. |
| `Logger` | `logger.Logger` | `nil` | Internal type; set with `WithLogger`. |

`NewQueue` validates the configuration and returns `config.ErrInvalidConfig`
(wrapped) when a field is out of range or unknown.

## Errors

| Package | Error | Returned when |
| --- | --- | --- |
| `gokue` | `"queue is nil"` | A method is called on a nil `*Queue`. Not an exported sentinel. |
| `gokue` | `"job cannot be nil"` | `Submit`/`TrySubmit` receives a nil job. Not an exported sentinel. |
| `gokue` | `"context cannot be nil"` | A nil context is passed. Not an exported sentinel. |
| `dispatcher` | `ErrClosed` | Submitting after the queue is closed. |
| `dispatcher` | `ErrQueueFull` | `TrySubmit` when the queue is at capacity. |
| `dispatcher` | `ErrNilJob` | Nil job at the dispatcher level. |
| `dispatcher` | `ErrNilCtx` | Nil context at the dispatcher level. |
| `dispatcher` | `ErrInvalidShutdownMode` | `CloseWithMode` receives an unknown mode. |
| `config` | `ErrInvalidConfig` | `NewQueue` receives invalid configuration. |
| `context` | `Canceled` / `DeadlineExceeded` | A submit or close context ends before the operation completes. |
| `backend` | `ErrNotFound`, `ErrAlreadyExists`, `ErrConflict`, `ErrStoreClosed`, `ErrNilStore`, `ErrNotConnected` | Storage operations; see the package docs. |
| `backend` | `ErrNilJob`, `ErrNameConflict`, `ErrJobNotRegistered`, `ErrEmptyPayload`, `ErrNoSerializableFields` | Codec registration, encoding, and decoding. |

## Concurrency and delivery semantics

- **Concurrency:** `Queue` methods and `dispatcher.Dispatcher` methods are safe
  for concurrent use. Statistics are updated with lock-free atomic counters.
- **Ordering:** jobs are enqueued in submission order into a buffered channel.
  With more than one worker, completion order is not guaranteed. A job that is
  retrying occupies its worker for the whole retry schedule, so it can hold up
  other jobs on that worker.
- **Attempts:** each job is attempted once, plus up to `MaxRetries` retries. A job
  that fails and is retried runs more than once; write jobs to be idempotent where
  that matters.
- **Durability:** none. The queue is in-memory, so queued and in-flight jobs are
  lost if the process exits unexpectedly. The `backend` package can persist jobs,
  but it is not wired into the queue.
- **Backpressure:** `Submit` blocks until enqueued or the context ends;
  `TrySubmit` returns `ErrQueueFull` immediately. Rejected submissions increment
  `Dropped`.
- **Panics:** a panic in a job's `Process` is recovered and reported as an error,
  which then goes through the normal retry and failure path. Panics inside
  dispatcher internals are recovered per worker so the process does not crash.
- **Shutdown:** in drain mode, `Close` waits for all jobs, including retries and
  their delays. If the close context ends first, it returns the context error and
  interrupts pending retry sleeps, but workers may still be finishing; do not tear
  down shared resources immediately after a cancelled `Close`.
- **Timeouts:** `JobTimeout` applies per attempt, not to the whole retry schedule.

## Testing

```
go test -race ./...
```

No external services are required. The `backend/mongodb` tests only validate
constructor and URI behavior; they do not connect to a MongoDB server.

CI runs `go vet`, `staticcheck`, and `go test -count=1 -race ./...` on the
`stable` and `oldstable` Go toolchains.

## Status

Available now:

- In-memory queue, worker pool, submission, retries and backoff, per-attempt
  timeout, panic recovery, statistics, optional logging, graceful shutdown.
- Standalone persistence package: `backend.JobStore`, `backend.MemoryStore`,
  `backend.Codec`, and the MongoDB connection/store in `backend/mongodb`.

Not yet available (see [`ROADMAP.md`](ROADMAP.md)):

- Queue integration with a storage backend (`MemoryType` abstraction).
- Dead-letter queue, job cancellation, priority levels, idempotency keys.
- Scheduled/delayed jobs, cron jobs, dynamic worker scaling.
- Result futures, typed generic jobs, job groups, batch submit, health checks,
  and a benchmark suite.

Known gaps in the current API:

- `WithMongoDB` and `config.MongoDB` are accepted by validation but have no
  effect; the queue always uses the in-memory channel.
- `WithShutdownTimeout` (and `Config.ShutdownTimeout`) is validated but not used;
  `Close` relies on the caller's context.
- `RetryPredicates` has no dedicated helper option; set it with a custom option.
- `NewQueueWithLogger` takes a `logger.Logger` from an `internal` package, so
  external callers cannot supply a custom logger; use `WithLogger`.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). Before opening a pull request, run:

```
go vet ./...
staticcheck ./...
go test -race ./...
```

## License

This repository does not currently include a `LICENSE` file. Add a license
before depending on or redistributing the project.
