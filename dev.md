# gokue — Development Plan & Issues

This document maps each item from `todo.txt` to GitHub issues. Labels appear in brackets before each issue title.

## Phase 1

### 1. [`enhancement`] Retry Predicates DONE

- **Issue Title:** Add retry predicates for conditional retry logic
- **Labels:** `enhancement`
- **Description:** Currently, gokue retries all failed jobs up to `MaxRetries`. A retry predicate would let users define a function that inspects the error returned by `job.Process(ctx)` and decides whether to retry. For example, "do not retry on `ErrValidation`" or "only retry on network errors". This is a common pattern in production job queues.
- **Acceptance Criteria:**
  - Users can set a global retry predicate and/or per-submit predicate
  - When predicate returns `false`, the job is marked failed immediately
  - When predicate returns `true`, normal retry logic applies
  - nil predicate means "always retry" (current behavior)
  - All existing tests continue to pass

### 2. [`enhancement`] Per-Job-Type Stat Breakdown

- **Issue Title:** Add per-job-type statistics breakdown
- **Labels:** `enhancement`
- **Description:** `stats.Snapshot` currently returns aggregate counters (enqueued, processed, failed, retried, dropped). Users need per-job-name visibility to see which job types are failing most, which are slowest, etc. This would break down stats by job name, optionally with latency histograms.
- **Acceptance Criteria:**
  - `Queue.Stats()` still returns aggregate totals (backward compat)
  - New method returns per-job-name breakdown
  - Counters correctly accumulate per job type
  - Thread-safe under concurrent submit/execute

### 3. [`enhancement`, `good-first-issue`] Structured Log Fields

- **Issue Title:** Allow custom structured log fields
- **Labels:** `enhancement`, `good-first-issue`
- **Description:** The logging package uses `LogEvent` with fixed fields (`Message`, `Level`, `JobName`, `Attempt`, `Error`, `Duration`). Users should be able to attach arbitrary key-value pairs to log events, e.g., `request_id`, `trace_id`, or custom metadata from the job. This enables better correlation in log aggregation systems.
- **Acceptance Criteria:**
  - Users can attach arbitrary key-value fields per job submission
  - Fields appear in all log events for that job (submit, processing, success, failure, retry)
  - No extra alloc when fields are not used
  - Backward compatible — existing log calls unchanged

### 4. [`enhancement`, `good-first-issue`] slog Adapter

- **Issue Title:** Implement `log/slog` adapter for the Logger interface
- **Labels:** `enhancement`, `good-first-issue`
- **Description:** Go 1.21 introduced `log/slog` as the standard structured logging library. gokue should provide a `SlogLogger` that wraps `*slog.Logger` and implements `logging.Logger`. This lets users who already use slog avoid bringing a second logger.
- **Acceptance Criteria:**
  - `SlogLogger` implements `logging.Logger`
  - All log output from gokue appears via the underlying `slog.Logger`
  - Level filtering works correctly (slog's built-in level handling)
  - Works with both `slog.NewJSONHandler` and `slog.NewTextHandler`

### 5. [`documentation`, `good-first-issue`] Godoc Examples

- **Issue Title:** Add runnable Godoc examples
- **Labels:** `documentation`, `good-first-issue`
- **Description:** Go's documentation convention uses `Example` functions that are compiled, run, and displayed on `pkg.go.dev`. Adding these makes the library more approachable and demonstrates common usage patterns.
- **Acceptance Criteria:**
  - `go test -run Example ./...` passes
  - Examples are clear, concise, and demonstrate real use cases
  - Output comments (`// Output:`) match actual program output

### 6. [`documentation`, `ops`, `good-first-issue`] Semver Releases + CHANGELOG

- **Issue Title:** Set up semantic versioning and CHANGELOG
- **Labels:** `documentation`, `ops`, `good-first-issue`
- **Description:** The project needs a versioning scheme and release process. The current state represents a solid MVP, making it a good time for `v0.1.0`. A `CHANGELOG.md` following Keep a Changelog convention helps users track changes between versions.
- **Acceptance Criteria:**
  - `v0.1.0` tagged and released on GitHub
  - CHANGELOG covers existing features
  - Future PRs add changelog entries

## Phase 2

### 7. [`enhancement`] Backend Abstraction Interface

- **Issue Title:** Define a backend abstraction interface
- **Labels:** `enhancement`
- **Description:** Currently, job storage is hard-coded to an in-memory Go channel inside `dispatcher.Dispatcher`. To support persistent backends (SQL, Redis, etc.), we need a clean `Backend` interface that encapsulates enqueue, dequeue, ack, and lifecycle operations. This is the foundation for all Phase 2 storage features (DLQ, cancellation, scheduling, etc.).
- **Acceptance Criteria:**
  - `Backend` interface is clean, minimal, and supports all current features
  - Dispatcher works identically with the in-memory backend
  - New backends can be implemented without touching dispatcher internals
  - All existing tests pass

### 8. [`enhancement`, `good-first-issue`] In-Memory Backend Implementation

- **Issue Title:** Implement in-memory backend as formal Backend implementation
- **Labels:** `enhancement`, `good-first-issue`
- **Description:** Once the `Backend` interface is defined (issue #7), move the current channel-based in-memory queue into a proper `MemoryBackend` struct. This separates concerns and validates that the interface is usable.
- **Acceptance Criteria:**
  - `MemoryBackend` implements `Backend` interface fully
  - All dispatcher/queue tests pass using `MemoryBackend`
  - Performance is comparable to direct channel approach

### 9. [`enhancement`] Dead-Letter Queue (DLQ)

- **Issue Title:** Add dead-letter queue for exhausted jobs
- **Labels:** `enhancement`
- **Description:** When a job exhausts all retries, it currently increments `Failed` and is dropped. A DLQ captures these failed jobs (with their error, attempt count, and metadata) for later inspection, replay, or alerting.
- **Acceptance Criteria:**
  - Failed jobs with exhausted retries go to DLQ (not just dropped)
  - DLQ preserves error, attempts, and timing metadata
  - Users can inspect, list, and replay dead letters
  - Bounded by configurable capacity
  - When DLQ is full, oldest entries are dropped or behavior is explicit

### 10. [`enhancement`] Job Cancellation

- **Issue Title:** Implement job cancellation (cancel queued or running jobs)
- **Labels:** `enhancement`
- **Description:** Users need to cancel a submitted job before it executes, or cancel a running job mid-execution. This requires tracking job IDs, mapping them to contexts, and allowing cancellation via the public API.
- **Acceptance Criteria:**
  - `Submit` returns a job ID that can be used for cancellation
  - Cancelling a queued job removes it from the queue
  - Cancelling a running job cancels its context
  - Cancelling a completed/failed job is a no-op
  - Thread-safe concurrent cancellation

### 11. [`enhancement`] Job Priority Levels

- **Issue Title:** Add job priority levels for ordering
- **Labels:** `enhancement`
- **Description:** Currently, jobs are processed in FIFO order. Priority levels would allow higher-priority jobs to be processed before lower-priority ones. This is typically implemented as a priority queue (heap) or multiple queues with weighted selection.
- **Acceptance Criteria:**
  - Jobs with higher priority are dequeued before lower priority
  - Same-priority jobs maintain FIFO order
  - Default priority is configurable per-queue and per-submit
  - No deadlock or starvation of low-priority jobs

### 12. [`enhancement`] Idempotency Key Dedup

- **Issue Title:** Implement idempotency key deduplication
- **Labels:** `enhancement`
- **Description:** Job queues can receive duplicate submissions (e.g., from retries in the caller, at-least-once delivery). An idempotency key allows callers to deduplicate: if a job with the same key is already in the queue (pending, running, or recently completed), the duplicate submission is silently dropped.
- **Acceptance Criteria:**
  - Submitting same idempotency key twice within TTL silently succeeds but only enqueues once
  - Different keys are unaffected
  - After TTL expires, the same key can be re-submitted
  - Thread-safe under concurrent submission

### 13. [`enhancement`] Scheduled / Delayed Jobs

- **Issue Title:** Add support for scheduled (delayed) job execution
- **Labels:** `enhancement`
- **Description:** Jobs should be submittable with a delay or a specific future execution time. The queue holds them until their scheduled time before making them available to workers. This requires a time-based scheduler that moves delayed jobs into the ready queue.
- **Acceptance Criteria:**
  - Jobs with `WithDelay(d)` execute at least `d` after submission
  - Jobs with `WithScheduledAt(t)` execute at or after `t`
  - Jobs with no delay execute immediately (current behavior)
  - Graceful shutdown waits for scheduled timer or drains properly

### 14. [`enhancement`] Cron / Recurring Jobs

- **Issue Title:** Add cron / recurring job scheduling
- **Labels:** `enhancement`
- **Description:** Jobs that execute on a fixed schedule (every 5 minutes, daily at midnight, etc.) using cron expressions. This is a foundational feature for maintenance tasks, periodic cleanup, polling, and batch processing.
- **Acceptance Criteria:**
  - Cron expressions are parsed correctly
  - Jobs fire at the correct schedule
  - `@every` shorthand works for simple intervals
  - Cron jobs use the same retry/timeout machinery as regular jobs
  - List/enumerate registered cron entries

### 15. [`enhancement`] Dynamic Worker Scaling

- **Issue Title:** Implement dynamic worker count scaling
- **Labels:** `enhancement`
- **Description:** Currently, worker count is fixed at queue creation. Dynamic scaling allows adjusting the number of workers at runtime (up or down) in response to queue depth, CPU load, or external signals.
- **Acceptance Criteria:**
  - `Resize(n)` increases or decreases active worker goroutines
  - Scaling down waits for in-flight jobs to finish before stopping workers
  - No jobs are lost during scale operations
  - Thread-safe concurrent `Resize` calls

### 16. [`enhancement`, `good-first-issue`] Functional Per-Submit Options (Extension)

- **Issue Title:** Extend per-submit options pattern to cover all job-level config
- **Labels:** `enhancement`, `good-first-issue`
- **Description:** With priority, idempotency, delay, scheduling, retry predicates, and log fields all becoming per-submit options, the `SubmitOption` pattern needs to be comprehensive and well-documented. This issue tracks adding `SubmitOption` implementations for every new Phase 2 feature and refactoring the option pattern for consistency.
- **Acceptance Criteria:**
  - Every per-job feature has a corresponding `SubmitOption`
  - Options compose correctly (last one wins for conflicting values)
  - Default (nil) option fields use queue-level config

## Phase 3

### 17. [`enhancement`] Result Futures

- **Issue Title:** Add result futures for asynchronous job results
- **Labels:** `enhancement`
- **Description:** Currently, `Submit` returns only an error (enqueue failed/succeeded). There's no way to get the return value or execution result of the job. A future/promise pattern lets callers submit a job and later await its result, including the returned value and any error.
- **Acceptance Criteria:**
  - `Future.Get()` blocks until job completes (success or final failure)
  - `Future.Get()` returns the error on failure, nil on success
  - `Future.Done()` is a channel that closes when job finishes
  - Future works with all existing features (retries, timeout, cancellation)

### 18. [`enhancement`] Typed Generic Jobs

- **Issue Title:** Add typed generic job support (Go 1.22+ generics)
- **Labels:** `enhancement`
- **Description:** The current `Job` interface uses `Process(context.Context) error`, which does not return a value. With Go generics, users could define `Job[T]` that returns `(T, error)`, providing type-safe job results.
- **Acceptance Criteria:**
  - `JobWithResult[T]` allows returning a typed value from `Process`
  - `SubmitTyped[T]` returns `*Future[T]` with the correct type
  - Backward compatible -- existing untyped jobs continue to work
  - Compile-time type safety enforced

### 19. [`enhancement`] Job Groups / Fan-Out

- **Issue Title:** Add job groups for fan-out parallel execution
- **Labels:** `enhancement`
- **Description:** A job group lets you submit multiple jobs together and wait for all of them to complete (fan-out). This is useful for parallel processing, scatter/gather, and batch operations where you need to dispatch N work items and collect results.
- **Acceptance Criteria:**
  - Jobs in a group are submitted concurrently to the queue
  - `Wait` returns results for all jobs (success or failure)
  - Context cancellation returns partial results
  - Groups compose with other features (retries, timeout, priority)

### 20. [`enhancement`] Batch Submit

- **Issue Title:** Add batch job submission
- **Labels:** `enhancement`
- **Description:** Submitting jobs one at a time has overhead (name validation, registration check, channel send, stats update per job). A `SubmitBatch` method enqueues multiple jobs in a single call, reducing lock contention and channel operations for high-throughput scenarios.
- **Acceptance Criteria:**
  - `SubmitBatch` submits multiple jobs efficiently
  - Returns per-job errors in same order as input
  - Consistent with existing submit semantics (retries, options)
  - Tested with empty batch, large batch, error cases

### 21. [`enhancement`, `good-first-issue`] Queue Health Check

- **Issue Title:** Add queue health check endpoint
- **Labels:** `enhancement`, `good-first-issue`
- **Description:** For production monitoring, the queue should expose a health check that verifies workers are alive, the queue is not stuck, and the backend is reachable. This is typically an HTTP endpoint for k8s probes, but gokue should provide the data programmatically too.
- **Acceptance Criteria:**
  - `Health()` returns status without side effects
  - Reports basic metrics: worker count, queue depth, backlog
  - Produces degraded/down status when appropriate
  - Usable for both programmatic monitoring and HTTP handlers

### 22. [`testing`, `good-first-issue`] Benchmark Suite

- **Issue Title:** Create performance benchmark suite
- **Labels:** `testing`, `good-first-issue`
- **Description:** Add Go benchmarks to measure throughput (jobs/sec), latency (P50/P99 enqueue-to-complete), worker scaling efficiency, and memory usage. These benchmarks serve as both performance documentation and regression prevention.
- **Acceptance Criteria:**
  - Benchmarks compile and run with `go test -bench=. ./...`
  - Results are reproducible and documented
  - Benchmarks cover happy path, retry path, and concurrency
  - No failures, races, or hangs in benchmarks

## Additional / Cross-Cutting Issues

### 23. [`bug`, `good-first-issue`] Fix Dispatcher Test File Name

- **Issue Title:** Fix typo in dispatcher test filename
- **Labels:** `bug`, `good-first-issue`
- **Description:** The dispatcher test file is named `dispatcjer_test.go` (missing the "e" in "dispatcher"). While Go doesn't require specific filenames, this is a typo that should be fixed for consistency.
- **Acceptance Criteria:**
  - File renamed, all tests still pass

### 24. [`ops`, `enhancement`] CI and Release Automation

- **Issue Title:** Enhance CI with release workflow and goreleaser
- **Labels:** `ops`, `enhancement`
- **Description:** The existing CI runs vet, staticcheck, and tests. Add a release workflow triggered by git tags that uses goreleaser (or a simple script) to publish GitHub releases. Also consider adding lint checks for CHANGELOG entries on PRs.
- **Acceptance Criteria:**
  - Tagging `v0.2.0` triggers an automated GitHub release
  - Release includes changelog, commit summary, and artifact (if applicable)
  - CI fails if CHANGELOG not updated (optional enforcement)

### 25. [`documentation`, `good-first-issue`] Comprehensive README Update

- **Issue Title:** Update README with full documentation and usage examples
- **Labels:** `documentation`, `good-first-issue`
- **Description:** The README should document all features, configuration options, API reference, and migration guides as the library grows. This is a living document that should be updated as each Phase is completed.
- **Acceptance Criteria:**
  - README covers all user-facing features
  - Examples are accurate and runnable
  - Clear installation/quickstart guide

## Label Reference

| Label | When to Use |
|-------|-------------|
| `enhancement` | New feature or capability |
| `documentation` | Docs, examples, README |
| `testing` | Benchmarks, test infrastructure |
| `ops` | CI, releases, tooling |
| `bug` | Defect or incorrect behavior |
| `good-first-issue` | Low-complexity, well-scoped task suitable for new contributors |
