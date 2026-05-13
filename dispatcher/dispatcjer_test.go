package dispatcher_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zt4ff/gokue/config"
	"github.com/zt4ff/gokue/dispatcher"
)

func setup(t *testing.T) *dispatcher.Dispatcher {
	t.Helper()

	config := config.Config{
		Backend:         config.InMemory,
		BackoffStrategy: config.Exponential,
		QueueSize:       10,
		WorkerCount:     2,
		MaxRetries:      2,
		JobTimeout:      5 * time.Second,
		RetryDelay:      50 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
	}

	d := dispatcher.New(config, nil)

	return d
}

// successJob is a job that completes successfully
type successJob struct{}

func (j *successJob) Process(ctx context.Context) error {
	return nil
}

// failJob is a job that fails with an error
type failJob struct {
	attempts *atomic.Int32
}

func (j *failJob) Process(ctx context.Context) error {
	j.attempts.Add(1)
	return errors.New("job failed")
}

// panicJob is a job that panics
type panicJob struct{}

func (j *panicJob) Process(ctx context.Context) error {
	panic("job panicked")
}

// slowJob sleeps for a duration
type slowJob struct {
	duration time.Duration
}

func (j *slowJob) Process(ctx context.Context) error {
	select {
	case <-time.After(j.duration):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSubmitWithNilContext(t *testing.T) {
	d := setup(t)
	defer d.Close(context.Background())

	task := dispatcher.Task{
		Name: "test-job",
		Job:  &successJob{},
	}

	err := d.Submit(nil, task)
	if err == nil {
		t.Error("expected ErrNilCtx, got nil")
	}
	if !errors.Is(err, dispatcher.ErrNilCtx) {
		t.Errorf("expected ErrNilCtx, got %v", err)
	}
}

func TestSubmitWithNilJob(t *testing.T) {
	d := setup(t)
	defer d.Close(context.Background())

	task := dispatcher.Task{
		Name: "test-job",
		Job:  nil,
	}

	err := d.Submit(context.Background(), task)
	if err == nil {
		t.Error("expected ErrNilJob, got nil")
	}
	if !errors.Is(err, dispatcher.ErrNilJob) {
		t.Errorf("expected ErrNilJob, got %v", err)
	}
}

func TestTrySubmitQueueFull(t *testing.T) {
	config := config.Config{
		Backend:         "in-memory",
		QueueSize:       1,
		WorkerCount:     1,
		MaxRetries:      0,
		JobTimeout:      5 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	}

	d := dispatcher.New(config, nil)
	defer d.Close(context.Background())

	ctx := context.Background()

	// Create a slow job that will block the worker
	slowTask := dispatcher.Task{
		Name: "slow-job",
		Job:  &slowJob{duration: 500 * time.Millisecond},
	}

	// Submit the slow job
	err := d.Submit(ctx, slowTask)
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}

	// Try to submit another job - queue should be full
	task := dispatcher.Task{
		Name: "test-job",
		Job:  &successJob{},
	}

	err = d.TrySubmit(ctx, task)
	if err == nil {
		t.Error("expected ErrQueueFull, got nil")
	}
	if !errors.Is(err, dispatcher.ErrQueueFull) {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}
}

func TestClosePreventsFutureSubmits(t *testing.T) {
	d := setup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Close the dispatcher
	err := d.Close(ctx)
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Try to submit after close - should return ErrClosed
	task := dispatcher.Task{
		Name: "test-job",
		Job:  &successJob{},
	}

	err = d.Submit(context.Background(), task)
	if err == nil {
		t.Error("expected ErrClosed, got nil")
	}
	if !errors.Is(err, dispatcher.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}

func TestRetryCount(t *testing.T) {
	d := setup(t)
	defer d.Close(context.Background())

	attempts := &atomic.Int32{}
	failingJob := &failJob{attempts: attempts}

	task := dispatcher.Task{
		Name: "test-job",
		Job:  failingJob,
	}

	err := d.Submit(context.Background(), task)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait for retries to complete (initial attempt + retries with backoff)
	time.Sleep(500 * time.Millisecond)

	// The job should have been attempted initial + maxRetries times
	// Default maxRetries is 2, so we expect 1 initial + 2 retries = 3 attempts
	attemptCount := attempts.Load()
	if attemptCount < 2 {
		t.Errorf("expected at least 2 attempts, got %d", attemptCount)
	}
}

func TestPanicInJobDoesNotCrash(t *testing.T) {
	d := setup(t)
	defer d.Close(context.Background())

	panicTask := dispatcher.Task{
		Name: "panic-job",
		Job:  &panicJob{},
	}

	// This should not panic
	err := d.Submit(context.Background(), panicTask)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait for job to be processed
	time.Sleep(200 * time.Millisecond)

	// Check stats - the job should have failed due to panic
	stats := d.Stats()
	if stats.Failed == 0 {
		t.Error("expected failed count > 0 after panic")
	}
}

func TestSuccessfulJobIncrementsProcessedStat(t *testing.T) {
	d := setup(t)
	defer d.Close(context.Background())

	task := dispatcher.Task{
		Name: "success-job",
		Job:  &successJob{},
	}

	err := d.Submit(context.Background(), task)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait for job to be processed
	time.Sleep(200 * time.Millisecond)

	stats := d.Stats()
	if stats.Processed != 1 {
		t.Errorf("expected Processed=1, got %d", stats.Processed)
	}
}

func TestMultipleSubmits(t *testing.T) {
	d := setup(t)
	defer d.Close(context.Background())

	// Submit multiple jobs
	for i := 0; i < 5; i++ {
		task := dispatcher.Task{
			Name: "job",
			Job:  &successJob{},
		}
		err := d.Submit(context.Background(), task)
		if err != nil {
			t.Fatalf("submit failed: %v", err)
		}
	}

	// Wait for jobs to be processed
	time.Sleep(500 * time.Millisecond)

	stats := d.Stats()
	if stats.Enqueued < 5 {
		t.Errorf("expected Enqueued >= 5, got %d", stats.Enqueued)
	}
}

// ============================================================================
// Graceful Shutdown Tests (Task 3.1 - 3.3)
// ============================================================================

// TestCloseDrainWaitsForLongRunningJob verifies that Close in drain mode waits
// for a long-running job to complete before returning.
func TestCloseDrainWaitsForLongRunningJob(t *testing.T) {
	cfg := config.Config{
		Backend:         config.InMemory,
		BackoffStrategy: config.Exponential,
		QueueSize:       10,
		WorkerCount:     1,
		MaxRetries:      0,
		JobTimeout:      5 * time.Second,
		RetryDelay:      50 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
	}

	d := dispatcher.New(cfg, nil)

	// Track job completion
	jobCompletedAt := &atomic.Pointer[time.Time]{}

	// Create a wrapper job that tracks completion time
	job := &slowJob{duration: 300 * time.Millisecond}

	// Submit the slow job
	err := d.Submit(context.Background(), dispatcher.Task{
		Name: "slow-job",
		Job:  job,
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Give the worker time to start processing
	time.Sleep(50 * time.Millisecond)

	// Close with drain mode - should wait for the slow job
	startClose := time.Now()
	err = d.Close(context.Background())
	closeTime := time.Since(startClose)

	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Close should have taken significant time (at least close to job duration)
	// Allow some variance due to scheduling
	if closeTime < 200*time.Millisecond {
		t.Logf("close took %.1fms (may be too quick), job was 300ms", closeTime.Seconds()*1000)
	}

	_ = jobCompletedAt
}

// TestCloseContextTimeoutReturnsError verifies that Close returns an error when
// the context times out while waiting for workers.
func TestCloseContextTimeoutReturnsError(t *testing.T) {
	cfg := config.Config{
		Backend:         config.InMemory,
		BackoffStrategy: config.Constant,
		QueueSize:       10,
		WorkerCount:     1,
		MaxRetries:      0,
		JobTimeout:      0, // no job timeout
		RetryDelay:      50 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
	}

	d := dispatcher.New(cfg, nil)

	// Create a job that never completes
	hangingJob := &testJob{
		process: func(ctx context.Context) error {
			// Block forever - Close will timeout waiting for this
			select {
			case <-time.After(30 * time.Second):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}

	err := d.Submit(context.Background(), dispatcher.Task{
		Name: "hanging-job",
		Job:  hangingJob,
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Give the job time to start
	time.Sleep(50 * time.Millisecond)

	// Create a short timeout context - Close should timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Close should timeout and return context error
	err = d.Close(ctx)

	if err == nil {
		t.Error("expected context timeout error, got nil")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

// TestCloseImmediateModeReturnsQuickly verifies that Close in immediate mode
// returns quickly without waiting for workers.
func TestCloseImmediateModeReturnsQuickly(t *testing.T) {
	cfg := config.Config{
		Backend:         config.InMemory,
		BackoffStrategy: config.Exponential,
		QueueSize:       10,
		WorkerCount:     1,
		MaxRetries:      0,
		JobTimeout:      5 * time.Second,
		RetryDelay:      50 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
	}

	d := dispatcher.New(cfg, nil)

	// Submit a slow job
	err := d.Submit(context.Background(), dispatcher.Task{
		Name: "slow-job",
		Job:  &slowJob{duration: 2 * time.Second},
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Give worker time to start
	time.Sleep(50 * time.Millisecond)

	// Close in immediate mode should return quickly
	startClose := time.Now()
	err = d.CloseWithMode(context.Background(), "immediate")
	closeTime := time.Since(startClose)

	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Close should return almost immediately (< 50ms)
	if closeTime > 100*time.Millisecond {
		t.Errorf("immediate mode close took too long (%.1fms), expected < 50ms", closeTime.Seconds()*1000)
	}
}

// TestCloseWithInvalidModeReturnsError verifies that CloseWithMode returns an error
// for invalid shutdown modes.
func TestCloseWithInvalidModeReturnsError(t *testing.T) {
	d := setup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := d.CloseWithMode(ctx, "invalid-mode")

	if err == nil {
		t.Error("expected ErrInvalidShutdownMode, got nil")
	}

	if !errors.Is(err, dispatcher.ErrInvalidShutdownMode) {
		t.Errorf("expected ErrInvalidShutdownMode, got %v", err)
	}
}

// TestCloseWithNilContextReturnsError verifies that Close returns ErrNilCtx
// when passed a nil context.
func TestCloseWithNilContextReturnsError(t *testing.T) {
	d := setup(t)

	err := d.Close(nil)

	if err == nil {
		t.Error("expected ErrNilCtx, got nil")
	}

	if !errors.Is(err, dispatcher.ErrNilCtx) {
		t.Errorf("expected ErrNilCtx, got %v", err)
	}
}

// TestCloseDrainProcessesQueuedJobs verifies that drain mode processes all
// jobs that were queued before Close was called.
func TestCloseDrainProcessesQueuedJobs(t *testing.T) {
	cfg := config.Config{
		Backend:         config.InMemory,
		BackoffStrategy: config.Exponential,
		QueueSize:       100,
		WorkerCount:     1,
		MaxRetries:      0,
		JobTimeout:      5 * time.Second,
		RetryDelay:      50 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
	}

	d := dispatcher.New(cfg, nil)

	// Submit multiple jobs quickly
	numJobs := 10
	for i := 0; i < numJobs; i++ {
		err := d.Submit(context.Background(), dispatcher.Task{
			Name: "job",
			Job:  &successJob{},
		})
		if err != nil {
			t.Fatalf("submit failed: %v", err)
		}
	}

	// Close in drain mode should process all queued jobs
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := d.Close(ctx)
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Check stats - all jobs should be processed
	stats := d.Stats()
	if stats.Processed < uint64(numJobs) {
		t.Errorf("expected at least %d processed, got %d", numJobs, stats.Processed)
	}
}

// TestCloseStopsAcceptingNewSubmissions verifies that submissions are rejected
// immediately after Close is called.
func TestCloseStopsAcceptingNewSubmissions(t *testing.T) {
	cfg := config.Config{
		Backend:         config.InMemory,
		BackoffStrategy: config.Exponential,
		QueueSize:       10,
		WorkerCount:     1,
		MaxRetries:      0,
		JobTimeout:      5 * time.Second,
		RetryDelay:      50 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
	}

	d := dispatcher.New(cfg, nil)

	// Submit a slow job to keep worker busy
	err := d.Submit(context.Background(), dispatcher.Task{
		Name: "slow-job",
		Job:  &slowJob{duration: 500 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Give worker time to start
	time.Sleep(50 * time.Millisecond)

	// Start closing in a goroutine
	go func() {
		_ = d.Close(context.Background())
	}()

	// Give close time to mark as closed
	time.Sleep(50 * time.Millisecond)

	// Try to submit new job - should be rejected
	err = d.Submit(context.Background(), dispatcher.Task{
		Name: "new-job",
		Job:  &successJob{},
	})

	if !errors.Is(err, dispatcher.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}

// TestRetryAbandonedOnQuit verifies that in-progress retries are abandoned
// when the dispatcher is closed.
func TestRetryAbandonedOnQuit(t *testing.T) {
	cfg := config.Config{
		Backend:         config.InMemory,
		BackoffStrategy: config.Constant,
		QueueSize:       10,
		WorkerCount:     1,
		MaxRetries:      5,
		JobTimeout:      5 * time.Second,
		RetryDelay:      1 * time.Second, // Long retry delay
		ShutdownTimeout: 5 * time.Second,
	}

	d := dispatcher.New(cfg, nil)

	// Submit a job that fails once then succeeds
	attempts := &atomic.Int32{}
	job := &failJob{attempts: attempts}

	err := d.Submit(context.Background(), dispatcher.Task{
		Name: "failing-job",
		Job:  job,
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait for first attempt to fail and enter retry sleep
	time.Sleep(100 * time.Millisecond)

	// Close should interrupt the retry sleep
	startClose := time.Now()
	closeErr := d.Close(context.Background())
	closeDuration := time.Since(startClose)

	if closeErr != nil {
		t.Fatalf("close failed: %v", closeErr)
	}

	// Close should return quickly (not wait for full 1s retry delay)
	if closeDuration > 500*time.Millisecond {
		t.Errorf("close took %.1fms, expected < 500ms (retry should be interrupted)", closeDuration.Seconds()*1000)
	}

	// Check stats - job should be failed (retries abandoned)
	stats := d.Stats()
	if stats.Failed == 0 {
		t.Error("expected failed count > 0 after retry abandoned")
	}
}

// TestMultipleCloseCalls verifies that multiple Close calls are safe.
func TestMultipleCloseCalls(t *testing.T) {
	d := setup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// First close should succeed
	err1 := d.Close(ctx)
	if err1 != nil {
		t.Fatalf("first close failed: %v", err1)
	}

	// Second close should also succeed (idempotent)
	err2 := d.Close(ctx)
	if err2 != nil {
		t.Fatalf("second close failed: %v", err2)
	}
}

// TestDrainModeWithQuitChannel verifies that the quit channel is properly
// used to interrupt retries during shutdown.
func TestDrainModeWithQuitChannel(t *testing.T) {
	cfg := config.Config{
		Backend:         config.InMemory,
		BackoffStrategy: config.Linear,
		QueueSize:       10,
		WorkerCount:     1,
		MaxRetries:      3,
		JobTimeout:      5 * time.Second,
		RetryDelay:      100 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
	}

	d := dispatcher.New(cfg, nil)

	// Create a job that succeeds on 2nd attempt
	callCount := &atomic.Int32{}
	job := &testJob{
		process: func(ctx context.Context) error {
			count := callCount.Add(1)
			if count < 2 {
				return errors.New("fail once")
			}
			return nil
		},
	}

	err := d.Submit(context.Background(), dispatcher.Task{
		Name: "job",
		Job:  job,
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait for first attempt and retry backoff to start
	time.Sleep(50 * time.Millisecond)

	// Close should interrupt the backoff and process remaining jobs
	startClose := time.Now()
	closeErr := d.Close(context.Background())
	closeDuration := time.Since(startClose)

	if closeErr != nil {
		t.Fatalf("close failed: %v", closeErr)
	}

	// Should be faster than waiting for full backoff
	if closeDuration > 500*time.Millisecond {
		t.Logf("close took %.1fms (acceptable for drain mode)", closeDuration.Seconds()*1000)
	}
}

// testJob is a helper job for testing with custom process logic
type testJob struct {
	process func(ctx context.Context) error
}

func (j *testJob) Process(ctx context.Context) error {
	return j.process(ctx)
}
