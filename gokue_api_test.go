package gokue_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zt4ff/gokue/v2"
	"github.com/zt4ff/gokue/v2/config"
)

type funcJob func(context.Context) error

func (f funcJob) Process(ctx context.Context) error { return f(ctx) }

type recordingLogger struct {
	mu    sync.Mutex
	count int
}

func (l *recordingLogger) Log(gokue.Level, string, ...interface{}) {
	l.mu.Lock()
	l.count++
	l.mu.Unlock()
}
func (l *recordingLogger) Debug(m string, f ...interface{}) { l.Log(gokue.LevelDebug, m, f...) }
func (l *recordingLogger) Info(m string, f ...interface{})  { l.Log(gokue.LevelInfo, m, f...) }
func (l *recordingLogger) Warn(m string, f ...interface{})  { l.Log(gokue.LevelWarn, m, f...) }
func (l *recordingLogger) Error(m string, f ...interface{}) { l.Log(gokue.LevelError, m, f...) }

func TestExternalCustomLogger(t *testing.T) {
	l := &recordingLogger{}
	q, err := gokue.NewQueueWithLogger(l, gokue.WithWorkerCount(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Submit(context.Background(), funcJob(func(context.Context) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.count == 0 {
		t.Fatal("custom logger received no events")
	}
}

func TestMongoDBRejectedUntilImplemented(t *testing.T) {
	_, err := gokue.NewQueue(gokue.WithMongoDB(config.Config{}))
	if !errors.Is(err, gokue.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestSentinelErrors(t *testing.T) {
	var nilQueue *gokue.Queue
	if err := nilQueue.Submit(context.Background(), funcJob(nil)); !errors.Is(err, gokue.ErrNilQueue) {
		t.Errorf("nil queue: got %v", err)
	}
	if err := nilQueue.Run(funcJob(nil)); !errors.Is(err, gokue.ErrNilQueue) {
		t.Errorf("nil queue Run: got %v", err)
	}

	q, err := gokue.NewQueue(gokue.WithWorkerCount(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Submit(context.Background(), nil); !errors.Is(err, gokue.ErrNilJob) {
		t.Errorf("nil job: got %v", err)
	}
	//lint:ignore SA1012 Intentionally passing a nil context to verify validation.
	if err := q.TrySubmit(nil, funcJob(nil)); !errors.Is(err, gokue.ErrNilCtx) {
		t.Errorf("nil ctx: got %v", err)
	}
	if err := q.Submit(context.Background(), funcJob(nil), gokue.MaxRetries(-1)); !errors.Is(err, gokue.ErrInvalidTask) {
		t.Errorf("invalid task: got %v", err)
	}
	if _, err := gokue.NewQueue(gokue.WithWorkerCount(0)); !errors.Is(err, gokue.ErrInvalidConfig) {
		t.Errorf("invalid config: got %v", err)
	}
	if err := q.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := q.Run(funcJob(func(context.Context) error { return nil })); !errors.Is(err, gokue.ErrClosed) {
		t.Errorf("Run after close: got %v", err)
	}
}

func TestQueueFullSentinel(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	q, err := gokue.NewQueue(gokue.WithWorkerCount(1), gokue.WithQueueSize(1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(release)
		_ = q.Close(context.Background())
	}()
	block := funcJob(func(context.Context) error {
		started <- struct{}{}
		<-release
		return nil
	})
	if err := q.Submit(context.Background(), block); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := q.Submit(context.Background(), funcJob(func(context.Context) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if err := q.TrySubmit(context.Background(), funcJob(nil)); !errors.Is(err, gokue.ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
}

func TestShutdownTimeoutBoundsClose(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	q, err := gokue.NewQueue(
		gokue.WithWorkerCount(1),
		gokue.WithJobTimeout(0),
		gokue.WithShutdownTimeout(50*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Submit(context.Background(), funcJob(func(context.Context) error {
		close(started)
		<-release
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	<-started

	start := time.Now()
	err = q.Close(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %v, ShutdownTimeout not applied", elapsed)
	}
}

var errPermanent = errors.New("permanent")

func TestRetryPredicateOptions(t *testing.T) {
	var attempts atomic.Int32
	failing := funcJob(func(context.Context) error {
		attempts.Add(1)
		return errPermanent
	})
	noRetry := func(err error) bool { return !errors.Is(err, errPermanent) }

	q, err := gokue.NewQueue(
		gokue.WithWorkerCount(1),
		gokue.WithMaxRetries(3),
		gokue.WithRetryDelay(0),
		gokue.WithRetryPredicate(noRetry),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Submit(context.Background(), failing); err != nil {
		t.Fatal(err)
	}
	// Per-submission override retries everything.
	if err := q.Submit(context.Background(), failing, gokue.RetryIf(func(error) bool { return true })); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 1 attempt for the queue-level predicate + 4 attempts for the override.
	if got := attempts.Load(); got != 5 {
		t.Fatalf("expected 5 attempts, got %d", got)
	}
	if s := q.Stats(); s.Failed != 2 || s.Retried != 3 {
		t.Fatalf("unexpected stats %+v", s)
	}
}

func TestCloseWithModeImmediateReturnsWithoutWaiting(t *testing.T) {
	q, err := gokue.NewQueue(gokue.WithWorkerCount(1))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	if err := q.Submit(context.Background(), funcJob(func(context.Context) error {
		close(started)
		<-release
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := q.Submit(context.Background(), funcJob(func(context.Context) error { return nil })); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- q.CloseWithMode(context.Background(), gokue.ImmediateMode) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CloseWithMode: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ImmediateMode waited for the running job")
	}
	if err := q.Submit(context.Background(), funcJob(func(context.Context) error { return nil })); !errors.Is(err, gokue.ErrClosed) {
		t.Fatalf("Submit after close = %v, want ErrClosed", err)
	}
}

func TestCloseWithModeRejectsUnknownMode(t *testing.T) {
	q, err := gokue.NewQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close(context.Background())
	if err := q.CloseWithMode(context.Background(), gokue.ShutdownMode("bogus")); !errors.Is(err, gokue.ErrInvalidShutdownMode) {
		t.Fatalf("got %v, want ErrInvalidShutdownMode", err)
	}
	var nilQ *gokue.Queue
	if err := nilQ.CloseWithMode(context.Background(), gokue.DrainMode); !errors.Is(err, gokue.ErrNilQueue) {
		t.Fatalf("nil queue: got %v", err)
	}
}
