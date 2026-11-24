package dispatcher_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zt4ff/gokue/config"
	"github.com/zt4ff/gokue/dispatcher"
	logging "github.com/zt4ff/gokue/internal/logger"
)

func regressionConfig(workers, queueSize int) config.Config {
	return config.Config{
		MemoryType:      config.InMemory,
		BackoffStrategy: config.Constant,
		QueueSize:       queueSize,
		WorkerCount:     workers,
		MaxRetries:      0,
		JobTimeout:      5 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	}
}

// waitFor polls cond until it returns true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// TestCloseNotBlockedBySubmitWaitingForCapacity verifies that a Submit blocked
// on a full queue does not prevent Close from honoring its context, and that
// the blocked Submit is released with ErrClosed.
func TestCloseNotBlockedBySubmitWaitingForCapacity(t *testing.T) {
	d := dispatcher.NewDispatcher(regressionConfig(1, 1), nil)
	release := make(chan struct{})
	defer close(release)

	ctx := context.Background()
	if err := d.Submit(ctx, dispatcher.Task{Job: &blockingJob{release: release}}); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let the worker pick up the first job
	if err := d.Submit(ctx, dispatcher.Task{Job: &blockingJob{release: release}}); err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	submitErr := make(chan error, 1)
	go func() {
		submitErr <- d.Submit(ctx, dispatcher.Task{Job: &successJob{}})
	}()
	time.Sleep(50 * time.Millisecond) // let the third submit block on the full queue

	closeCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	closeErr := make(chan error, 1)
	go func() { closeErr <- d.Close(closeCtx) }()

	select {
	case err := <-closeErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected DeadlineExceeded from Close, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close ignored its context while a Submit was blocked on a full queue")
	}

	select {
	case err := <-submitErr:
		if !errors.Is(err, dispatcher.ErrClosed) {
			t.Fatalf("expected ErrClosed from blocked Submit, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Submit was not released by Close")
	}
}

// TestImmediateModeDropsQueuedJobs verifies that ImmediateMode does not keep
// executing jobs that were still waiting in the queue.
func TestImmediateModeDropsQueuedJobs(t *testing.T) {
	d := dispatcher.NewDispatcher(regressionConfig(1, 10), nil)
	release := make(chan struct{})

	ctx := context.Background()
	if err := d.Submit(ctx, dispatcher.Task{Job: &blockingJob{release: release}}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	var ran atomic.Int32
	for i := 0; i < 3; i++ {
		if err := d.Submit(ctx, dispatcher.Task{Job: &countingJob{ran: &ran}}); err != nil {
			t.Fatalf("submit queued: %v", err)
		}
	}

	if err := d.CloseWithMode(ctx, dispatcher.ImmediateMode); err != nil {
		t.Fatalf("close: %v", err)
	}
	close(release)

	if !waitFor(t, time.Second, func() bool { s := d.Stats(); return s.Processed+s.Dropped >= 4 }) {
		t.Fatalf("workers did not settle, stats: %+v", d.Stats())
	}
	if got := ran.Load(); got != 0 {
		t.Errorf("expected queued jobs to be dropped, %d ran", got)
	}
	if s := d.Stats(); s.Processed != 1 || s.Dropped != 3 {
		t.Errorf("expected Processed=1 Dropped=3, got %+v", s)
	}
}

// TestNegativeTaskOverridesRejected verifies that invalid per-task overrides
// are rejected at submission instead of silently failing the job unexecuted.
func TestNegativeTaskOverridesRejected(t *testing.T) {
	d := dispatcher.NewDispatcher(regressionConfig(1, 10), nil)
	defer d.Close(context.Background())

	negRetries := -1
	negDelay := -time.Second
	cases := map[string]dispatcher.Task{
		"max retries": {Job: &successJob{}, MaxRetries: &negRetries},
		"retry delay": {Job: &successJob{}, RetryDelay: &negDelay},
	}
	for name, task := range cases {
		if err := d.Submit(context.Background(), task); !errors.Is(err, dispatcher.ErrInvalidTask) {
			t.Errorf("Submit %s: expected ErrInvalidTask, got %v", name, err)
		}
		if err := d.TrySubmit(context.Background(), task); !errors.Is(err, dispatcher.ErrInvalidTask) {
			t.Errorf("TrySubmit %s: expected ErrInvalidTask, got %v", name, err)
		}
	}
	if s := d.Stats(); s.Enqueued != 0 || s.Failed != 0 {
		t.Errorf("expected no enqueued or failed jobs, got %+v", s)
	}
}

// TestWorkerKeepsRunningAfterInternalPanic verifies that a panic in dispatcher
// internals does not permanently remove the worker from the pool.
func TestWorkerKeepsRunningAfterInternalPanic(t *testing.T) {
	cfg := regressionConfig(1, 10)
	cfg.Logger = &panicOnEventLogger{panicOn: string(logging.EventJobFailed)}
	d := dispatcher.NewDispatcher(cfg, nil)
	defer d.Close(context.Background())

	ctx := context.Background()
	if err := d.Submit(ctx, dispatcher.Task{Job: &failJob{attempts: &atomic.Int32{}}}); err != nil {
		t.Fatalf("submit failing job: %v", err)
	}
	if err := d.Submit(ctx, dispatcher.Task{Job: &successJob{}}); err != nil {
		t.Fatalf("submit success job: %v", err)
	}

	if !waitFor(t, time.Second, func() bool { return d.Stats().Processed == 1 }) {
		t.Fatalf("worker died after internal panic, stats: %+v", d.Stats())
	}
}

type countingJob struct {
	ran *atomic.Int32
}

func (j *countingJob) Process(context.Context) error {
	j.ran.Add(1)
	return nil
}

// TestRetryPredicateNotConsultedOnFinalAttempt verifies the predicate is skipped
// when no retry remains, so RetryPredicatesFailed only counts blocked retries.
func TestRetryPredicateNotConsultedOnFinalAttempt(t *testing.T) {
	var calls atomic.Int32
	cfg := config.Default()
	cfg.WorkerCount = 1
	cfg.MaxRetries = 0
	cfg.RetryDelay = 0
	cfg.RetryPredicates = func(error) bool {
		calls.Add(1)
		return false
	}
	d := dispatcher.NewDispatcher(cfg, nil)
	if err := d.Submit(context.Background(), dispatcher.Task{
		Job: jobFunc(func(context.Context) error { return errors.New("boom") }),
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("predicate called %d times with no retries left", calls.Load())
	}
	if s := d.Stats(); s.RetryPredicatesFailed != 0 || s.Failed != 1 {
		t.Fatalf("unexpected stats %+v", s)
	}
}

type jobFunc func(context.Context) error

func (f jobFunc) Process(ctx context.Context) error { return f(ctx) }
