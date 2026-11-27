# Changelog

All notable changes to gokue are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[semantic versioning](https://semver.org). See CONTRIBUTING.md for which
packages are covered by the compatibility promise.

## [Unreleased]

### Added

- `Queue.AdminHandler` serves `GET /stats`, `GET /dead` (the dead-letter queue,
  without payloads) and `POST /dead/{id}/replay` as JSON for operators (#47).

## [2.2.0] - 2026-11-26

### Added

- `backend/mongodb` integration tests against a real MongoDB server, run in CI
  and locally when `GOKUE_MONGODB_URI` is set (#31).
- Dead-letter queue for persistent queues: `Queue.DeadJobs` lists jobs that
  failed for good and `Queue.Replay` runs one again with its attempts reset.
  `backend.JobRecord` gains `LastError`, the final error of a failed record,
  saved by the MongoDB store as `last_error`. New errors `ErrNotPersistent` and
  `ErrNotFailed` (#33).
- Examples: `examples/http-offload` (HTTP server handing work to a queue) and
  `examples/persistence` (a queue running jobs left in the store by an earlier
  one). CI now runs the examples (#52).

### Fixed

- The MongoDB module path is now `github.com/zt4ff/gokue/backend/mongodb/v2`.
  The path announced in 2.1.0, `github.com/zt4ff/gokue/v2/backend/mongodb`,
  could not be installed because Go requires the major-version suffix at the
  end of a module path. Import the new path and install it with
  `go get github.com/zt4ff/gokue/backend/mongodb/v2`.

## [2.1.0] - 2026-11-26

### Added

- Persistent queues: `WithStore(store, codec)` saves jobs to a
  `backend.JobStore` on submit and workers claim them from it, giving
  at-least-once delivery and recovery of jobs left unfinished by a crash.
  `WithLease` and `WithPollInterval` tune it. `config.Config` gains `Store`,
  `Codec`, `Lease`, `PollInterval` and the `config.Persistent` memory type.
  The in-memory queue is unchanged. See ADR 0001 (#28, #29).
- README: delivery guarantees per backend (#34).
- README: "Why gokue?" comparison with asynq, River, gocraft/work and errgroup (#49).

### Changed

- `backend/mongodb` is now its own Go module,
  `github.com/zt4ff/gokue/v2/backend/mongodb`, so the core module no longer
  depends on the MongoDB driver. The import path is unchanged; projects that
  import it need `go get github.com/zt4ff/gokue/v2/backend/mongodb` (#30).

## [2.0.0] - 2026-11-26

First release covered by this changelog. It includes everything in v1.0.1 plus
the changes below. Because of the breaking changes, the module path is now
`github.com/zt4ff/gokue/v2`:

```sh
go get github.com/zt4ff/gokue/v2@v2.0.0
```

and imports change from `github.com/zt4ff/gokue/...` to
`github.com/zt4ff/gokue/v2/...`.

### Features (carried over from v1.0.1)

- Bounded in-memory queue with a fixed pool of worker goroutines.
- Blocking `Submit`, non-blocking `TrySubmit`, and fire-and-forget `Run`.
- Retries with `Constant`, `Linear`, `Exponential` and `ExponentialJitter`
  backoff, per-attempt `JobTimeout`, and per-submission retry overrides.
- Job panic recovery, atomic `Stats` counters, optional structured JSON logging,
  and graceful drain-on-close shutdown.
- `backend.JobStore` interface with in-memory and MongoDB implementations (not
  yet wired into `Queue`).

### Added

- Lifecycle hooks `WithOnSuccess`, `WithOnFailure`, `WithOnRetry` receiving a
  `gokue.JobEvent` (#24).
- Live `QueueDepth`, `Running` and `IdleWorkers` gauges in `stats.Snapshot` (#26).
- `Queue.CloseWithMode` with `gokue.DrainMode` / `gokue.ImmediateMode` (#23).
- `WithRetryPredicate` and per-submission `RetryIf` options (#16).
- Public `gokue.Logger` / `gokue.Level` so external code can pass its own logger
  to `NewQueueWithLogger` (#14).
- Sentinel errors on `gokue` (`ErrClosed`, `ErrQueueFull`, `ErrNilJob`,
  `ErrNilCtx`, `ErrInvalidTask`, `ErrInvalidConfig`, `ErrInvalidShutdownMode`,
  `ErrNilQueue`) usable with `errors.Is` (#15).
- `*gokue.PanicError` with the panic value and stack trace; panics are logged as
  a `job_panic` event including the stack (#25).
- Job contexts carry the values of the `Submit` context (not its cancellation) (#25).
- Runnable Godoc examples (#6) and a benchmark suite with published numbers (#27).

### Changed

- **Breaking:** module path is `github.com/zt4ff/gokue/v2` (#7).
- **Breaking:** the `dispatcher` package moved to `internal/dispatcher`; use the
  re-exported `gokue` types instead (#23).
- **Breaking:** `Queue.Run` returns an `error` (#17).
- **Breaking:** `Close` now honours `ShutdownTimeout` (default 10s) when its
  context has no deadline; `WithShutdownTimeout(0)` waits forever (#13).
- **Breaking:** `WithMongoDB` / `config.MongoDB` make `NewQueue` fail with
  `ErrInvalidConfig` instead of silently staying in memory; `WithMongoDB` is
  deprecated until persistence is wired in (#12).
- Shutdown modes are a typed `ShutdownMode` (#20).
- `MaxRetryDelay` caps every backoff strategy, not just exponential (#19).
- The retry predicate is not called when no retries remain (#18).
- The logger closer no longer closes `os.Stdout` / `os.Stderr` (#60).

### Fixed

- `Close` ignored its timeout while a `Submit` was blocked on a full queue.
- `ImmediateMode` still ran every queued job.
- A negative per-submission `MaxRetries` skipped the job but counted it failed.
- A panic inside the dispatcher killed the worker permanently.
- Exponential backoff overflowed without `MaxRetryDelay` (negative delay, or a
  panic with jitter).
- MongoDB `Claim` returned jobs in nondeterministic order.
- Zero-delay retries still ran after `Close` timed out (#59).
- The logger closer failed with `EINVAL` when stdout is a pipe (#60).
- `examples/multiple-jobs` panicked on shutdown (#61).

### Removed

- `todo.txt` and the `.vscode/` directory from the repository (#22).

## [1.0.1] - 2026-10-12

- License update.

## [1.0.0] - 2026-10-12

- Initial tagged version.

[Unreleased]: https://github.com/zt4ff/gokue/compare/v2.2.0...HEAD
[2.2.0]: https://github.com/zt4ff/gokue/compare/v2.1.0...v2.2.0
[2.1.0]: https://github.com/zt4ff/gokue/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/zt4ff/gokue/compare/v1.0.1...v2.0.0
[1.0.1]: https://github.com/zt4ff/gokue/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/zt4ff/gokue/releases/tag/v1.0.0
