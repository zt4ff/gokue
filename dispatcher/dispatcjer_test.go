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
