# ADR 0002: Delayed jobs and retries that do not hold a worker

- Status: Proposed
- Issue: [#35](https://github.com/zt4ff/gokue/issues/35)

## Context

A failing job sleeps inside its worker between retries
(`Dispatcher.execute` → `shutdownBeforeRetry`). With 4 workers and a 30s
backoff, four failing jobs stop the queue for 30s. There is also no way to
submit a job that should run later.

The same is true in persistent mode (ADR 0001): a retry waits in the worker
while it holds the lease, and a crash during the wait loses the retry schedule.

Goals:

1. `Submit` can delay a job (`Delay`) or schedule it at a time (`RunAt`).
2. A retry waits for its backoff without occupying a worker.
3. Persistent queues keep delayed jobs and pending retries across restarts.
4. Existing code that uses neither option behaves the same, apart from the
   worker no longer being blocked during backoff.

Non-goals: cron/recurring jobs (#39), cancellation (#36), priorities (#37).

## Decision

### Public API

```go
q.Submit(ctx, Report{}, gokue.Delay(10*time.Minute))
q.Submit(ctx, Report{}, gokue.RunAt(time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)))
```

- `Delay(d time.Duration) SubmitOption` and `RunAt(t time.Time) SubmitOption`.
  They follow the existing `SubmitOption` names (`MaxRetries`, `RetryDelay`,
  `RetryIf`); `With*` is reserved for queue `Option`s. A zero or past time
  runs the job as soon as possible. Negative `Delay` is `ErrInvalidTask`.
- `stats.Snapshot` gains a `Scheduled` gauge: jobs waiting for their run time
  (delayed submissions and retries in backoff). `Running` no longer includes
  jobs waiting between retries.

### In-memory queues

- A scheduler goroutine owns a min-heap of `(runAt, task, attempt)`. At the
  due time it sends the task into the existing tasks channel, so due jobs
  compete for workers like any other job and respect `QueueSize`.
- `execute` runs one attempt. On a retryable failure it computes the backoff
  as today, fires `OnRetry`, increments `Retried`, and hands the task to the
  scheduler with `attempt+1`; the worker is then free. Per-submit overrides
  travel with the task.
- Capacity: delayed submissions count against `QueueSize` together with
  queued jobs, so `TrySubmit(..., Delay(...))` returns `ErrQueueFull` and
  `Submit` blocks when `queued + scheduled >= QueueSize`. Retries of jobs
  already accepted are never refused.
- Health (#45) treats scheduled jobs the same way when computing saturation.

### Persistent queues

- `backend.JobRecord` gains `RunAt *time.Time` (nil means now).
- `JobStore.Claim` contract: claim only pending records whose `RunAt` is nil
  or not after now, oldest `RunAt`/`CreatedAt` first. `MemoryStore` and
  `MongoJobStore` implement it (Mongo adds an index on `status, run_at`).
- `Delay`/`RunAt` set `RunAt` on save.
- A retry sets the record back to `pending` with `RunAt = now + backoff`
  (attempt count is already in `Attempts`) and releases the worker. The retry
  survives a crash or restart. Retry overrides are still not persisted
  (ADR 0001), so a retry picked up by another process uses queue settings.
- Idle workers poll at `PollInterval`, so a delayed job runs up to one poll
  interval late; `Submit` with no delay still wakes a worker immediately.

### Shutdown

- `Close`/`DrainMode` waits, bounded by its context and `ShutdownTimeout`,
  until nothing is queued, running or scheduled. Retries in backoff are
  therefore still waited for, as today. Delayed jobs are waited for too, so a
  job delayed by an hour holds `Close` until the context ends.
- When the context ends, or with `ImmediateMode`, jobs still scheduled are
  dropped and counted in `Dropped`; a dropped retry fires `OnFailure` as an
  abandoned job does today.
- Persistent queues do not wait for scheduled work: it is already in the
  store and the next process runs it. `Close` only finishes jobs this process
  is running, as in ADR 0001.
- Internally the tasks channel is no longer closed to stop workers (the
  scheduler may still send). Workers stop on `quit`, which Drain signals once
  the outstanding count reaches zero.

## Alternatives considered

- **Keep sleeping in the worker and only add `Delay`.** Smallest change, but
  does not meet the "workers are not blocked by backoff" criterion.
- **A timer goroutine per delayed job (`time.AfterFunc`).** Simple, but one
  goroutine and timer per job, and harder to drain and count.
- **Drop delayed jobs on Drain without waiting.** Faster shutdown, but
  silently loses work for in-memory users; waiting until the context ends is
  the safer default and the context bounds it.
- **A separate scheduled collection/status in the store.** More moving parts
  than a `RunAt` field on pending records.

## Consequences

- Custom `JobStore` implementations must honour `RunAt` in `Claim`; one that
  ignores it runs delayed jobs and retries early (no other harm). This is a
  documented contract change, not a compile-time break.
- `Running` changes meaning slightly (excludes backoff); `Scheduled` is new.
- `backend/mongodb` needs a release requiring the matching core version.
- The dispatcher's shutdown path is rewritten, so this lands as one PR with
  race tests for Drain/Immediate with scheduled retries and delayed jobs.
