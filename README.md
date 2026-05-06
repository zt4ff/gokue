# gokue

`gokue` is a bounded in-memory job queue with worker pools, per-job timeouts, retries, and atomic runtime stats.

## Features

- Bounded queue with backpressure
- Worker pool sized by configuration
- Job timeouts and retry delays
- Panic recovery per job execution
- Atomic queue metrics

## Basic Usage

```go
q, err := gokue.NewQueue(
	gokue.WithWorkerCount(4),
	gokue.WithQueueSize(1024),
	gokue.WithMaxRetries(3),
	gokue.WithJobTimeout(30*time.Second),
)
if err != nil {
	return err
}
defer q.Close(context.Background())

q.RegisterJob("send email")

if err := q.Submit(context.Background(), "send email", myJob); err != nil {
	return err
}
```

## Submit Methods

- `Submit` blocks until the job is accepted or the context is canceled.
- `TrySubmit` returns immediately with `dispatcher.ErrQueueFull` when the queue has no capacity.

## Shutdown

Call `Close` with a context to wait for workers to drain in-flight jobs. Use a timeout context for bounded shutdown in production.
