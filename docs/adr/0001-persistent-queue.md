# ADR 0001: Persisting queued jobs through backend.JobStore

- Status: Proposed
- Issue: [#28](https://github.com/zt4ff/gokue/issues/28) (implementation: [#29](https://github.com/zt4ff/gokue/issues/29))

## Context

`gokue.Queue` keeps jobs in a buffered channel, so a crash or restart loses
every queued and running job. `backend.JobStore` (with `MemoryStore` and
`mongodb.MongoJobStore`) and `backend.Codec` already exist but nothing in the
queue uses them, and `WithMongoDB` is rejected by `NewQueue`.

Goals:

1. Jobs submitted to a persistent queue survive a process restart and run at
   least once.
2. The channel path stays the default, with no new dependencies and no
   behaviour change for existing users.
3. Any `JobStore` implementation works without dispatcher changes.

Non-goals here: delayed/scheduled jobs (#35), cancellation (#36), dead-letter
inspection and replay (#33), moving backends to their own modules (#30).

## Decision

### Public API

```go
store := backend.NewMemoryStore() // or a mongodb.MongoJobStore
codec := backend.NewCodec()
_ = codec.Register("", SendEmail{}) // every persisted job type is registered

q, err := gokue.NewQueue(
    gokue.WithStore(store, codec),
    gokue.WithLease(2*time.Minute),        // default 5m
    gokue.WithPollInterval(time.Second),   // default 1s
)
```

- `WithStore(store backend.JobStore, codec *backend.Codec) Option` switches the
  queue to persistent mode. Both arguments are required (`ErrInvalidConfig`
  otherwise).
- `config.Config` gains `Store`, `Codec`, `Lease` and `PollInterval`.
- The queue does not own the store: the caller closes it after `Queue.Close`.
- `WithMongoDB` / `config.MongoDB` stay deprecated and rejected; the MongoDB
  path is `WithStore(mongoStore, codec)`.

### Submit

`Submit`, `TrySubmit` and `Run` encode the job with the codec, generate a random
`job.ID`, and `Save` a `StatusPending` record. The call returns once the record
is saved; an unregistered job type returns the codec error
(`backend.ErrJobNotRegistered`). Submit then sends a non-blocking wake-up to idle
workers.

The store is the queue, so `QueueSize` does not apply: `Submit` never blocks on
capacity and `TrySubmit` never returns `ErrQueueFull`.

### Workers: claim, run, record the outcome

In persistent mode each worker loops:

1. `Claim` the oldest pending record. On `ErrNotFound` wait for a wake-up, the
   poll interval (to see jobs saved by other processes) or shutdown.
2. Decode the payload. A decode failure marks the record `StatusFailed`.
3. Run the job with the existing retry loop (backoff, predicate, hooks, stats
   all unchanged). While it runs, a heartbeat refreshes `StartedAt` every
   `Lease/3` through `Update`, so a long job is not taken for a crashed one.
4. On success set `StatusSucceeded`; when retries are exhausted or the predicate
   refuses, set `StatusFailed`. `FinishedAt` is set in both cases. Records are
   kept, not deleted, so #33 can inspect and replay failed ones.

Only the worker that claimed a record writes to it. Writes use the record's
`Version`, so a worker whose lease was taken over gets `ErrConflict`, logs it,
and leaves the record to the new owner.

The channel and `QueueDepth` are not used in persistent mode; `QueueDepth`
reports 0 and the other stats are unchanged.

### Recovering from crashes

A reaper calls `RequeueStale(lease)` when the queue starts and then every
`Lease/2`. Records stuck in `StatusRunning` for longer than the lease (their
process died before the heartbeat could refresh them) go back to pending and
are claimed again. Several processes can share one store; `Claim` is atomic in
both shipped stores.

### Shutdown

- `DrainMode` stops claiming, waits for jobs already claimed (including their
  retries) and leaves unclaimed pending records in the store for the next
  start. "Drain" means "finish what this process took", not "empty the store".
- `ImmediateMode`, or a drain whose context expires, abandons retries; the
  abandoned record is put back to `StatusPending` so it runs again later.
  Running jobs that are not waited for are recovered by the reaper after the
  lease.

### Delivery semantics (for the README, #34)

| Mode | Guarantee | A job can be lost when | A job can run twice when |
| --- | --- | --- | --- |
| In-memory (default) | at most once | the process exits with jobs queued or running | never |
| `WithStore` | at least once | the store loses data | the process dies after the job ran but before its outcome was saved, or a job outlives its lease without heartbeats |

Jobs used with a store should be idempotent.

### What is not persisted

Per-submit `MaxRetries`, `RetryDelay` and `RetryIf` apply to the attempt run by
the process that accepted the job. After a crash the recovered job uses the
queue-level settings. `RetryIf` cannot be serialized; the other two can be
added to `JobRecord` later without breaking the API.

`JobRecord.Attempts` counts claims (deliveries), not retries inside one claim.

## Alternatives considered

- **Keep the channel and add a write-ahead copy in the store.** Two sources of
  truth, and a job recovered from the store could also still be in the channel
  after a reconnect. Rejected.
- **A feeder goroutine that claims into the channel.** Claims jobs before a
  worker is free, so their leases tick while they sit in the buffer. Workers
  claiming directly avoids this.
- **Persist every retry as a separate pending record with a run-at time.**
  Frees the worker during backoff, but needs `RunAt` in `JobRecord` and both
  stores; that is #35's work and can replace the in-worker retry loop then.
- **Delete records on success.** Smaller collections, but loses the audit trail
  and the base for #33. A retention option can be added later.

## Consequences

- Existing users see no change; persistence is opt-in.
- Jobs must be JSON-serializable through `Codec` (exported fields or
  `json.Marshaler`), and every type must be registered before `Submit`.
- Users need idempotent jobs for at-least-once delivery.
- The worker loop gains a second mode; it stays in `internal/dispatcher`, so the
  public API only grows by `WithStore`, `WithLease` and `WithPollInterval`.
