# gokue

<p align="center">
  <img src="./gokue.png" alt="Gokue Logo" width="180" />
</p>

`gokue` is a small, dependency-light job queue for Go. It runs jobs on a fixed
worker pool with a bounded in-memory queue, and provides per-attempt timeouts,
retries with configurable backoff, panic recovery, atomic statistics, and
optional structured logging.

[![Go Reference](https://pkg.go.dev/badge/github.com/zt4ff/gokue/v2.svg)](https://pkg.go.dev/github.com/zt4ff/gokue/v2)
[![CI](https://github.com/zt4ff/gokue/actions/workflows/ci.yml/badge.svg)](https://github.com/zt4ff/gokue/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/zt4ff/gokue/branch/main/graph/badge.svg)](https://codecov.io/gh/zt4ff/gokue)

## Overview

`gokue` is an in-process job queue. Jobs are plain Go values that implement a
single `Process` method; the queue schedules them across a bound pool of worker
goroutines and applies retry, timeout, and panic-handling policy. It is intended
for background work within a single Go service — sending email, resizing images,
calling an API, and similar best-effort tasks — where adding an external broker
would be unnecessary weight.

By default the queue is **in-memory**: jobs do not survive a process restart,
and no external service is required. With `WithStore`, jobs are saved to a
`backend.JobStore` and unfinished jobs run again after a crash or restart; see
[Persistent queues](#persistent-queues).

## Features

- Bounded in-memory queue with a configurable capacity (the default).
- Fixed pool of worker goroutines.
- Blocking (`Submit`), non-blocking (`TrySubmit`), and fire-and-forget (`Run`) submission.
- Retries with `Constant`, `Linear`, `Exponential`, and `ExponentialJitter` backoff.
- Per-attempt job timeout and per-submission retry overrides.
- Config-level retry predicate to decide whether a given error is retryable.
- Job panic recovery; a panicking job is treated as a failure.
- Atomic, lock-free statistics snapshot via `Stats`: cumulative counters plus live `QueueDepth`, `Running` and `IdleWorkers` gauges.
- Optional structured JSON logging via `WithLogger`.
- Graceful shutdown that drains running and queued jobs (plus `CloseWithMode(ctx, gokue.ImmediateMode)` for an immediate stop).
- Optional persistence through `WithStore`: at-least-once delivery, leases and
  crash recovery on top of a `backend.JobStore` (in-memory and MongoDB stores
  are included).

## Why gokue?

gokue runs background jobs inside your Go process. The default queue needs no
broker, database or extra service; a store can be added later for jobs that
must survive a restart.

| | gokue | [asynq](https://github.com/hibiken/asynq) | [River](https://github.com/riverqueue/river) | [gocraft/work](https://github.com/gocraft/work) | [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup) |
| --- | --- | --- | --- | --- | --- |
| Needs | nothing (a `JobStore` for persistence) | Redis | PostgreSQL or SQLite | Redis | nothing |
| Jobs survive a restart | with `WithStore` | yes | yes | yes | no |
| Workers in other processes | only through a shared store | yes | yes | yes | no |
| Retries and backoff | yes | yes | yes | yes | no |
| Delayed and cron jobs | not yet | yes | yes | yes | no |
| Priorities, unique jobs | not yet | yes | yes | yes | no |
| Enqueue in a database transaction | no | no | yes | no | no |
| Web UI | no | yes | yes | yes | no |

Choose gokue when:

- the work belongs to one service and you do not want to run Redis or
  PostgreSQL just for a queue;
- you want retries, backoff, timeouts, panic recovery, hooks and stats around a
  bounded worker pool, which `errgroup` or a hand-written pool does not give
  you;
- you want to start in memory and add persistence later without changing how
  jobs are written.

Choose something else when:

- separate worker processes or machines must share the queue at scale: use
  asynq or River (gocraft/work's last release, v0.5.1, is from 2018);
- a job must be enqueued atomically with other database writes: use River;
- you need delayed, cron, priority or unique jobs today (they are on the
  [roadmap](ROADMAP.md)), or a dashboard;
- you only need to run a few goroutines and collect the first error: use
  `errgroup`.

The comparison was checked in November 2026 against asynq v0.26.0, River
v0.51.2, gocraft/work v0.5.1 and the `errgroup` package documentation.

## Installation

```
go get github.com/zt4ff/gokue/v2@latest
```

Requires Go **1.22.2** or newer (the version declared in `go.mod`).

The MongoDB store is a separate module, so the core module does not pull in the
MongoDB driver. Add it only if you use it:

```
go get github.com/zt4ff/gokue/backend/mongodb/v2@latest
```

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/zt4ff/gokue/v2"
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

`gokue.Queue` is the public entry point. It owns an internal dispatcher, which
runs the worker pool and the retry loop. The dispatcher lives under `internal/`
and is not part of the public API; everything it exposes to users (errors,
`ShutdownMode`, `PanicError`, `CloseWithMode`) is re-exported from `gokue`.

```go
func NewQueue(options ...Option) (*Queue, error)
func (q *Queue) Submit(ctx context.Context, task Job, opts ...SubmitOption) error
func (q *Queue) TrySubmit(ctx context.Context, task Job, opts ...SubmitOption) error
func (q *Queue) Run(task Job, opts ...SubmitOption) error
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
field can be set directly with a custom option.

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
Pass a store and a codec to `gokue.WithStore` to make a queue persistent; see
[Persistent queues](#persistent-queues).

## Usage

### Defining a job

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zt4ff/gokue/v2"
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
never blocks and returns `gokue.ErrQueueFull` when the queue is at capacity.
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
if errors.Is(err, gokue.ErrQueueFull) {
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
by `MaxRetryDelay` (the cap applies to every strategy). `MaxRetries` and
`RetryDelay` can be overridden per submission. A retry predicate is only
consulted when another retry remains.

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

To stop retrying for specific errors, set a retry predicate. `RetryIf`
overrides it for a single submission:

```go
q, err := gokue.NewQueue(gokue.WithRetryPredicate(func(err error) bool {
	return errors.Is(err, errTemporary)
}))
if err != nil {
	log.Fatal(err)
}

err = q.Submit(ctx, job, gokue.RetryIf(func(error) bool { return true }))
```

When the predicate returns `false`, the job is marked failed immediately and the
`RetryPredicatesFailed` statistic is incremented.

### Lifecycle hooks

`WithOnSuccess`, `WithOnFailure` and `WithOnRetry` call a function with a
`gokue.JobEvent` (`Name`, `Attempt`, `Err`, `Duration`, `RetryDelay`). For each
job, `OnRetry` fires before every retry, then exactly one of `OnSuccess` or
`OnFailure` fires. `OnFailure` covers exhausted retries, a retry predicate
returning `false`, and retries abandoned during shutdown. Hooks run on the
worker goroutine, so keep them fast; a panicking hook is recovered.

```go
q, err := gokue.NewQueue(
	gokue.WithOnFailure(func(e gokue.JobEvent) {
		log.Printf("job %s failed after %d attempts: %v", e.Name, e.Attempt, e.Err)
	}),
)
```

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

If the close context ends first, jobs still waiting in the queue are dropped
(counted in `Dropped`) and pending retries are abandoned. A `Submit` blocked on a
full queue is released with `gokue.ErrClosed` as soon as `Close` is called.

For a complete program that wires SIGINT/SIGTERM to `Close`, see
[examples/graceful-shutdown](examples/graceful-shutdown/main.go).

For an immediate return that does not wait for workers, use `CloseWithMode`
with `gokue.ImmediateMode` (`Close` is `CloseWithMode` with `gokue.DrainMode`):

```go
q.CloseWithMode(context.Background(), gokue.ImmediateMode)
```

### Error handling

Errors from the queue are compared with `errors.Is`.

```go
switch {
case errors.Is(err, gokue.ErrClosed):
	// The queue has been closed.
case errors.Is(err, gokue.ErrQueueFull):
	// TrySubmit found the queue at capacity.
case errors.Is(err, context.Canceled):
	// The submit context was cancelled while waiting.
case errors.Is(err, context.DeadlineExceeded):
	// The submit context or the shutdown context timed out.
}
```

### Logging

Logging is disabled by default. `WithLogger` returns an option plus a closer that
flushes and closes the writer (`os.Stdout` and `os.Stderr` are flushed but left
open, and pipes or terminals that cannot be synced don't cause an error); call
the closer when the queue is no longer needed.

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

### Persistent queues

By default jobs live in memory and are lost when the process exits. With
`WithStore`, `Submit` saves each job to a `backend.JobStore` and workers claim
jobs from it, so a job left unfinished by a crash or restart runs again.

```go
codec := backend.NewCodec()
if err := codec.Register("", EmailJob{}); err != nil { // every job type you submit
	log.Fatal(err)
}
store := backend.NewMemoryStore() // or a mongodb.MongoJobStore

q, err := gokue.NewQueue(
	gokue.WithStore(store, codec),
	gokue.WithLease(2*time.Minute),      // default 5m
	gokue.WithPollInterval(time.Second), // default 1s
)
if err != nil {
	log.Fatal(err)
}
defer store.Close(context.Background()) // the queue does not close the store
defer q.Close(context.Background())

err = q.Submit(ctx, EmailJob{To: "a@example.com", Body: "hi"})
```

What changes compared with the in-memory queue:

- **Jobs must be serializable.** They are stored as JSON, so only exported
  fields survive, and each type must be registered in the codec. `Submit`
  returns `backend.ErrJobNotRegistered` for an unregistered type.
- **Delivery is at least once.** A job can run twice if the process dies after
  the job ran but before its outcome was saved. Write jobs to be idempotent.
- **Leases.** A claimed job renews its lease while it runs. If its process
  dies, the job goes back to pending once the lease expires and another worker
  (in this or another process sharing the store) runs it.
- **No queue capacity.** `QueueSize` is ignored, `Submit` does not block on a
  full queue, `TrySubmit` never returns `ErrQueueFull`, and `Stats().QueueDepth`
  is 0.
- **Shutdown.** `Close` finishes the jobs this process already claimed and
  leaves the rest in the store for the next start. A retry abandoned by
  shutdown goes back to pending.
- **Records are kept.** Finished records stay in the store as `succeeded` or
  `failed`; delete them with `JobStore.Delete` if you do not need the history.
- **Dead letters.** `failed` records are the dead-letter queue: jobs that ran
  out of retries, were refused by a retry predicate, or could not be decoded.
  `Queue.DeadJobs` lists them with the final error in `LastError`, and
  `Queue.Replay` puts one back in the queue with its attempts reset:

  ```go
  dead, err := q.DeadJobs(ctx, 100)
  for _, rec := range dead {
  	log.Printf("%s %s failed: %s", rec.ID, rec.Name, rec.LastError)
  	if err := q.Replay(ctx, rec.ID); err != nil {
  		log.Print(err)
  	}
  }
  ```

  Both return `gokue.ErrNotPersistent` on a queue without `WithStore`, whose
  failed jobs are not kept (use `WithOnFailure` to capture them).
- **Not saved:** per-submit options (`MaxRetries`, `RetryDelay`, `RetryIf`)
  apply only when the submitting process runs the job; a job picked up by
  another process or after a restart uses the queue-level settings. The submit
  context's values are not passed to the job.

`backend.MemoryStore` keeps records in process memory, so it is useful for
tests and for sharing one store between queues, not for surviving a restart.
The design is described in
[ADR 0001](docs/adr/0001-persistent-queue.md).

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
| `WithMaxRetryDelay` | `time.Duration` | `30s` | Cap for every backoff strategy. `<= 0` means no cap. |
| `WithBackoffStrategy` | `config.BackoffStrategy` | `Exponential` | One of `Constant`, `Linear`, `Exponential`, `ExponentialJitter`. |
| `WithLogger` | `io.Writer` | disabled | Enables structured JSON logging; also returns a closer. |
| `WithShutdownTimeout` | `time.Duration` | `10s` | Bounds `Close` when its context has no deadline. `0` waits indefinitely. |
| `WithRetryPredicate` | `func(error) bool` | `nil` | Retry a failed job only when it returns `true`. |
| `WithOnSuccess` / `WithOnFailure` / `WithOnRetry` | `func(gokue.JobEvent)` | `nil` | Lifecycle hooks; see [Lifecycle hooks](#lifecycle-hooks). |
| `WithStore` | `backend.JobStore`, `*backend.Codec` | in-memory | Saves jobs to the store; see [Persistent queues](#persistent-queues). Both arguments are required. |
| `WithLease` | `time.Duration` | `5m` | With `WithStore`: time without a heartbeat before a claimed job is run again. Must be `> 0`. |
| `WithPollInterval` | `time.Duration` | `1s` | With `WithStore`: how often idle workers check the store. Must be `> 0`. |
| `WithMongoDB` | `config.Config` | — | Deprecated; `NewQueue` returns `ErrInvalidConfig`. Use `WithStore` with a `mongodb.MongoJobStore`. |

### Per-submission options

| Option | Argument | Description |
| --- | --- | --- |
| `MaxRetries` | `int` | Overrides the queue-level `MaxRetries` for this job. Must be `>= 0`. |
| `RetryDelay` | `time.Duration` | Overrides the queue-level `RetryDelay` for this job. Must be `>= 0`. |
| `RetryIf` | `func(error) bool` | Overrides the queue-level retry predicate for this job. |

Invalid per-submission values make `Submit`/`TrySubmit` return a wrapped
`ErrInvalidTask`.

### `config.Config` fields

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `MemoryType` | `config.MemoryType` | `config.InMemory` | `InMemory`, or `Persistent` (set by `WithStore`). `MongoDB` is rejected. |
| `Store` / `Codec` | `backend.JobStore` / `*backend.Codec` | `nil` | Set with `WithStore`. |
| `Lease` / `PollInterval` | `time.Duration` | `5m` / `1s` | Used only with a store. |
| `WorkerCount` | `int` | `GOMAXPROCS(0)` | Must be `> 0`. |
| `QueueSize` | `int` | `1024` | Must be `> 0`. |
| `MaxRetries` | `int` | `3` | Must be `>= 0`. |
| `JobTimeout` | `time.Duration` | `30s` | Must be `>= 0`. |
| `RetryDelay` | `time.Duration` | `250ms` | Must be `>= 0`. |
| `MaxRetryDelay` | `time.Duration` | `30s` | Must be `>= 0`. |
| `BackoffStrategy` | `config.BackoffStrategy` | `Exponential` | Must be a known strategy. |
| `ShutdownTimeout` | `time.Duration` | `10s` | Bounds `Close` when its context has no deadline. |
| `RetryPredicates` | `func(error) bool` | `nil` | Set with `WithRetryPredicate`; `true` retries. |
| `Logger` | `gokue.Logger` | `nil` | Set with `WithLogger` or `NewQueueWithLogger`. |

`NewQueue` validates the configuration and returns `config.ErrInvalidConfig`
(wrapped) when a field is out of range or unknown.

## Errors

| Package | Error | Returned when |
| --- | --- | --- |
| `gokue` | `ErrNilQueue` | A method is called on a nil `*Queue`. |
| `gokue` | `ErrClosed` | Submitting after the queue is closed. |
| `gokue` | `ErrQueueFull` | `TrySubmit` when the queue is at capacity. |
| `gokue` | `ErrNilJob` / `ErrNilCtx` | A nil job or context is passed. |
| `gokue` | `ErrInvalidTask` | A per-submission override is out of range (e.g. negative `MaxRetries`). |
| `gokue` | `ErrInvalidShutdownMode` | `CloseWithMode` receives an unknown mode. |
| `gokue` | `ErrInvalidConfig` | Alias of `config.ErrInvalidConfig`. |
| `config` | `ErrInvalidConfig` | `NewQueue` receives invalid configuration. |
| `context` | `Canceled` / `DeadlineExceeded` | A submit or close context ends before the operation completes. |
| `backend` | `ErrNotFound`, `ErrAlreadyExists`, `ErrConflict`, `ErrStoreClosed`, `ErrNilStore`, `ErrNotConnected` | Storage operations; see the package docs. |
| `backend` | `ErrNilJob`, `ErrNameConflict`, `ErrJobNotRegistered`, `ErrEmptyPayload`, `ErrNoSerializableFields` | Codec registration, encoding, and decoding. |

## Concurrency and delivery semantics

- **Concurrency:** `Queue` methods are safe for concurrent use. Statistics are updated with lock-free atomic counters.
- **Ordering:** jobs are enqueued in submission order into a buffered channel.
  With more than one worker, completion order is not guaranteed. A job that is
  retrying occupies its worker for the whole retry schedule, so it can hold up
  other jobs on that worker.
- **Attempts:** each job is attempted once, plus up to `MaxRetries` retries. A job
  that fails and is retried runs more than once; write jobs to be idempotent where
  that matters.
- **Durability and delivery:** depends on the backend.

  | Queue | Guarantee | A job is lost when | A job runs twice when |
  | --- | --- | --- | --- |
  | In-memory (default) | at most once | the process exits with jobs queued or running | never (retries aside) |
  | `WithStore` + `MemoryStore` | at least once within the process | the process exits | a job stops renewing its lease while still running |
  | `WithStore` + `mongodb.MongoJobStore` | at least once | MongoDB loses the record | the process dies after the job ran but before its outcome was saved, or a job stops renewing its lease |

  The rest of this list describes the in-memory queue; see
  [Persistent queues](#persistent-queues) for the differences.
- **Job context:** `Process` receives a context that carries the values of the
  `Submit` context (request IDs, trace spans) but not its cancellation or
  deadline, so cancelling the submit context after `Submit` returns does not
  cancel the job. `JobTimeout` and shutdown still apply.
- **Backpressure:** `Submit` blocks until enqueued or the context ends;
  `TrySubmit` returns `ErrQueueFull` immediately. Rejected submissions increment
  `Dropped`.
- **Panics:** a panic in a job's `Process` is recovered and reported as a
  `*gokue.PanicError` (with the panic value and stack trace, also logged as a
  `job_panic` event), which then goes through the normal retry and failure path. Panics inside
  dispatcher internals are recovered per job, so the process does not crash and
  the worker keeps serving the queue.
- **Shutdown:** in drain mode, `Close` waits for all jobs, including retries and
  their delays. If the close context ends first, it returns the context error and
  interrupts pending retry sleeps, but workers may still be finishing; do not tear
  down shared resources immediately after a cancelled `Close`.
- **Timeouts:** `JobTimeout` applies per attempt, not to the whole retry schedule.

## Testing

```
go test -race ./...
```

No external services are required. `backend/mongodb` is its own Go module, so
run its tests from that directory (`cd backend/mongodb && go test -race ./...`);
they only validate constructor and URI behavior and do not connect to a MongoDB
server.

CI runs `go vet`, `staticcheck`, and `go test -count=1 -race ./...` on the
`stable` and `oldstable` Go toolchains.

## Benchmarks

The benchmarks live in [`gokue_bench_test.go`](gokue_bench_test.go) and use
no-op jobs, so they measure queue overhead rather than job work. Run them with:

```
go test -run='^$' -bench=. -benchmem -count=6 .
```

| Benchmark | What it measures |
| --- | --- |
| `Submit` | `Submit` from one goroutine while the default worker pool drains the queue. |
| `SubmitParallel` | `Submit` from `GOMAXPROCS` goroutines at once. |
| `TrySubmit` | `TrySubmit`, retried while the queue is full, so each op is one enqueued job. |
| `Throughput/workers=N` | Submit `b.N` jobs and wait for all to complete; reports enqueue-to-complete latency under load. |
| `EndToEndLatency` | One job at a time through an idle queue: submit, run, complete. |
| `Retry` | Jobs that fail once and succeed on an immediate retry (`RetryDelay` 0). |

Results on an 8-vCPU Intel Xeon VM (linux/amd64, Go 1.23.4, default options
unless noted), medians of 6 runs from
[`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat):

| Benchmark | ns/op | jobs/s | p50 latency | p99 latency | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `Submit` | 806 | 1.24M | — | — | 792 | 12 |
| `SubmitParallel` | 984 | 1.02M | — | — | 792 | 12 |
| `TrySubmit` | 797 | 1.26M | — | — | 792 | 12 |
| `Throughput/workers=1` | 1,064 | 940k | 980 µs | 1.73 ms | 792 | 12 |
| `Throughput/workers=4` | 757 | 1.32M | 62 µs | 501 µs | 792 | 12 |
| `Throughput/workers=16` | 767 | 1.31M | 62 µs | 204 µs | 792 | 12 |
| `EndToEndLatency` | 1,794 | — | 1.4 µs | 5.0 µs | 792 | 12 |
| `Retry` | 837 | 1.20M | — | — | 1,472 | 22 |

Allocations are per job and include execution, not just the `Submit` call.
Latency in `Throughput` includes time spent waiting in the queue: with one
worker the 1024-slot queue stays full, so p50 is roughly 1024 jobs' worth of
processing. Numbers vary by machine; compare runs on the same host with
`benchstat`.

CI runs every benchmark once per push on the `stable` toolchain as a smoke
test; there is no regression gate.

## Status

Available now:

- In-memory queue, worker pool, submission, retries and backoff, per-attempt
  timeout, panic recovery, statistics, optional logging, graceful shutdown.
- Persistent queues through `WithStore` with at-least-once delivery and crash
  recovery.
- A benchmark suite with published numbers (see [Benchmarks](#benchmarks)).
- Persistence package: `backend.JobStore`, `backend.MemoryStore`,
  `backend.Codec`, and the MongoDB connection/store in `backend/mongodb`.

Not yet available (see [`ROADMAP.md`](ROADMAP.md)):

- Dead-letter queue, job cancellation, priority levels, idempotency keys.
- Scheduled/delayed jobs, cron jobs, dynamic worker scaling.
- Result futures, typed generic jobs, job groups, batch submit, and health
  checks.

Known gaps in the current API:

- `WithMongoDB` is deprecated and rejected by `NewQueue`; pass a
  `mongodb.MongoJobStore` to `WithStore` instead. The MongoDB store has not
  been tested against a live server yet.

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
