# gokue Detailed TODO Guide



## 4) Deploy to backend

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
