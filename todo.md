# gokue Detailed TODO Guide

## 1) Add tests before refactoring (highest priority)

Why first: tests protect you from breaking behavior.

### 1.1 Create test files
- `dispatcher/dispatcher_test.go`
- `gokue_test.go`

### 1.2 Test cases to add

#### In `dispatcher/dispatcher_test.go`
- [ ] `Submit` with nil context returns `ErrNilCtx`
- [ ] `Submit` with nil job returns `ErrNilJob`
- [ ] `TrySubmit` returns `ErrQueueFull` when queue is full
- [ ] `Close` makes future submits return `ErrClosed`
- [ ] Retry count: failed job retries up to `MaxRetries`
- [ ] Panic in job does not crash process and increments failed stats

#### In `gokue_test.go`
- [ ] `Run` does not panic and submits anonymous job
- [ ] `Stats` returns non-zero values after processing

### 1.3 Helpful test pattern
- Create small fake jobs:
  - success job (returns nil)
  - fail job (returns error)
  - panic job (panics)
- Use channels or atomics to assert call counts.

### 1.4 Run tests often
```bash
go test ./...
```

Definition of done:
- [ ] Core behavior is covered by tests
- [ ] `go test ./...` passes consistently

---

## 2) Refactor retry/backoff logic (small and safe)

Current behavior is linear retry delay. Make it configurable.

### 2.1 Add backoff strategy type
In config layer, add a strategy setting:
- `constant`
- `linear`
- `exponential`
- `exponential-jitter`

### 2.2 Implement delay calculator
In dispatcher layer, create one function that takes:
- base delay
- attempt
- strategy

Return `time.Duration`.

### 2.3 Keep defaults backward-compatible
- Existing users should still get current behavior unless they opt in.

### 2.4 Add tests
- [ ] Delay values are correct for each strategy
- [ ] Delay never becomes negative
- [ ] Jitter strategy stays within expected range

Definition of done:
- [ ] Configurable backoff works
- [ ] Tests prove it

---

## 3) Improve graceful shutdown behavior

You already have `Close(ctx)`; make behavior explicit and tested.

### 3.1 Define expected modes
- Drain mode: finish queued/in-flight jobs
- Immediate mode: stop accepting new jobs and return quickly

If you only support one mode now, document exactly what it does.

### 3.2 Improve cancellation wiring
- Ensure `ctx` cancellation is respected during close waiting.
- Ensure submitters get deterministic errors during shutdown.

### 3.3 Add tests
- [ ] `Close` waits for long-running job in drain behavior
- [ ] `Close` returns `ctx.Err()` when close context times out
- [ ] No goroutine leaks in normal close path

Definition of done:
- [ ] Close behavior is deterministic and documented

---

## 4) Add CI (so you stop breaking main by accident)

Create GitHub workflow:
- file: `.github/workflows/ci.yml`

### CI checks
- `go test ./...`
- `go vet ./...`
- formatting check (`gofmt`)

Optional later:
- `golangci-lint`

### Commands to verify locally first
```bash
go test ./...
go vet ./...
gofmt -l .
```

Definition of done:
- [ ] CI runs automatically on PRs
- [ ] CI fails on format/test issues

---

## 5) Introduce backend abstraction (major refactor; do after tests)

Why: right now queueing is tied to in-memory channel behavior.

### 5.1 Create interface (internal/private first)
Add an internal backend contract with operations like:
- push task
- pop task
- close backend

Keep this minimal first. Don’t add Ack/Nack until you need durable backends.

### 5.2 Implement in-memory backend
- Wrap existing channel logic.
- Keep behavior same as today.

### 5.3 Update dispatcher to depend on backend interface
- Dispatcher should not know if tasks come from channel, DB, or broker.

### 5.4 Add compatibility tests
- Existing tests should still pass with in-memory backend.

Definition of done:
- [ ] Dispatcher works through interface
- [ ] No external API break unless intentional

---

## 6) Add observability (metrics + logs + tracing)

### 6.1 Metrics first
- Reuse `stats` counters.
- Add clear metric names for:
  - enqueued
  - processed
  - failed
  - retried
  - dropped

### 6.2 Structured logging
- Add logs at key points:
  - submit accepted/rejected
  - retry attempt
  - final failure
  - close start/complete

Include fields: job name, attempt, error, duration.

### 6.3 Tracing (later)
- Add OpenTelemetry spans around submit and job execution.

Definition of done:
- [ ] You can answer “what is the queue doing right now?” from logs + stats

---

## 7) Add production queue features (after core is stable)

### 7.1 Dead-letter queue (DLQ)
- Failed after all retries → move to DLQ store/queue.
- Include reason and attempt count.

### 7.2 Visibility timeout semantics (if using durable backend)
- Claimed jobs must reappear if worker crashes before ack.

### 7.3 Delivery semantics
Document what you guarantee:
- At-most-once
- At-least-once (most practical first)
- Exactly-once (usually requires idempotency + dedup)

Definition of done:
- [ ] Failure handling is explicit and recoverable

---

## 8) Performance and scaling tasks

### 8.1 Add benchmarks
Create benchmark tests for:
- low/medium/high worker count
- fast and slow jobs
- retry-heavy scenarios

Run:
```bash
go test -bench=. ./...
```

### 8.2 Use queueing basics for sizing
Use Little’s Law: $L = \lambda W$
- $\lambda$: arrival rate (jobs/sec)
- $W$: average processing time (sec)
- $L$: average jobs in system

Use this to pick initial `WorkerCount` and `QueueSize`.

Definition of done:
- [ ] You have measured throughput/latency data, not guesses

---

## 9) Security and robustness checklist

- [ ] Validate job names (`trim`, length limit, allowed chars)
- [ ] Document that jobs should be idempotent (important for retries)
- [ ] Keep panic recovery in execution path
- [ ] Add max timeout guidance for jobs
- [ ] Avoid unbounded memory growth

Definition of done:
- [ ] Bad inputs and bad jobs fail safely

---

## 10) Suggested PR order (copy this workflow)

PR 1: tests only
- add test files and coverage for current behavior

PR 2: CI only
- add workflow and make checks pass

PR 3: retry strategy refactor
- no backend changes yet

PR 4: backend interface + in-memory impl
- keep behavior unchanged

PR 5: shutdown behavior improvements

PR 6: observability additions

PR 7+: advanced features (DLQ, scheduling, rate limiting, persistence)

---

## 11) Research links that map directly to your roadmap

- Queueing theory basics: https://en.wikipedia.org/wiki/Queueing_theory
- RabbitMQ concepts (ack/nack, routing, prefetch, DLQ mindset): https://www.rabbitmq.com/tutorials/amqp-concepts.html
- AWS SQS concepts (visibility timeout, at-least-once, DLQ): https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/welcome.html
- Redis Streams docs: https://redis.io/docs/reference/streams/
- Celery architecture docs: https://docs.celeryq.dev/en/stable/userguide/architecture.html

---

## 12) Final “am I ready?” checklist

- [ ] I can run tests and they pass
- [ ] I can explain shutdown behavior clearly
- [ ] I know current delivery guarantees
- [ ] I can point to retry policy in code
- [ ] I can observe queue health from stats/logs
- [ ] My changes are in small reviewable PRs

If all checked, you are building this like a production engineer.
