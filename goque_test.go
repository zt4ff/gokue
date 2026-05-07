package gokue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zt4ff/gokue/config"
)

// successJob is a job that always completes successfully
type successJob struct {
	processed *atomic.Bool
}

func (j *successJob) Process(ctx context.Context) error {
	j.processed.Store(true)
	return nil
}

// failJob is a job that always fails
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

// job implements Job and tracks execution
type job struct {
	t *testing.T
}

func (j job) Process(ctx context.Context) error {
	j.t.Helper()
	return nil
}

func TestNewQueueInvalidConfigs(t *testing.T) {
	testcases := map[string]struct {
		config func(any) Option
		arg    any
	}{
		"with invalid backend option": {
			config: func(a any) Option { return WithConfig(a.(config.Config)) },
			arg: config.Config{
				Backend: "wrong backend",
			},
		},
		"with invalid backoff strategy": {
			config: func(a any) Option { return WithConfig(a.(config.Config)) },
			arg: config.Config{
				BackoffStrategy: "quadratic",
			},
		},
		"with worker count": {
			config: func(a any) Option { return WithWorkerCount(a.(int)) },
			arg:    -2,
		},
		"with queue size": {
			config: func(a any) Option { return WithQueueSize(a.(int)) },
			arg:    -1,
		},
		"with max retries": {
			config: func(a any) Option { return WithMaxRetries(a.(int)) },
			arg:    -1,
		},
		"with job timeout": {
			config: func(a any) Option { return WithJobTimeout(a.(time.Duration)) },
			arg:    time.Second * -1,
		},
		"with retry delay": {
			config: func(a any) Option { return WithRetryDelay(a.(time.Duration)) },
			arg:    time.Second * -1,
		},
		"with shutdown timeout": {
			config: func(a any) Option { return WithShutdownTimeout(a.(time.Duration)) },
			arg:    time.Second * -1,
		},
	}

	for name, testcase := range testcases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewQueue(testcase.config(testcase.arg))
			if err == nil {
				t.Error("expected error but got nil")
			}
		})
	}
}

func TestUnkownJob(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Errorf("err shouldn't be nil")
	}

	queue.RegisterJob("email runner")

	err = queue.Submit(context.Background(), "non-existent-job", job{t: t})
	if err == nil {
		t.Errorf("expected error but got nil")
	}
}

func TestRunJob(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	success := &successJob{processed: &atomic.Bool{}}

	// Run should not panic and should submit the job
	queue.Run(success)

	// Give some time for the job to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify the job was processed
	if !success.processed.Load() {
		t.Error("expected job to be processed")
	}
}

func TestStats(t *testing.T) {
	queue, err := NewQueue(
		WithQueueSize(10),
		WithWorkerCount(2),
		WithMaxRetries(2),
		WithJobTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	// Register and submit a job
	successJob := &successJob{processed: &atomic.Bool{}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = queue.Submit(ctx, "test-job", successJob)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait for job to be processed
	time.Sleep(200 * time.Millisecond)

	// Check stats
	stats := queue.Stats()
	if stats.Enqueued == 0 {
		t.Error("expected Enqueued > 0")
	}
	if stats.Processed == 0 {
		t.Error("expected Processed > 0")
	}
}

func TestSubmitWithNilContext(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	successJob := &successJob{processed: &atomic.Bool{}}
	err = queue.Submit(nil, "test-job", successJob)
	if err == nil {
		t.Error("expected error when context is nil")
	}
	if err.Error() != "context cannot be nil" {
		t.Errorf("expected 'context cannot be nil', got %v", err)
	}
}

func TestSubmitWithNilJob(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ctx := context.Background()
	err = queue.Submit(ctx, "test-job", nil)
	if err == nil {
		t.Error("expected error when job is nil")
	}
	if err.Error() != "job cannot be nil" {
		t.Errorf("expected 'job cannot be nil', got %v", err)
	}
}

func TestTrySubmitQueueFull(t *testing.T) {
	queue, err := NewQueue(WithQueueSize(1), WithWorkerCount(1))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	ctx := context.Background()

	// Use a job that blocks to fill the queue
	blockingJob := make(chan struct{})
	blockingTask := &blockingJobImpl{blockChan: blockingJob}

	// Submit the blocking job to fill the queue
	err = queue.Submit(ctx, "blocking", blockingTask)
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}

	// Now try to submit another job without blocking - should fail
	successJob := &successJob{processed: &atomic.Bool{}}
	err = queue.TrySubmit(ctx, "test-job", successJob)
	if err == nil {
		t.Error("expected ErrQueueFull")
	}

	// Clean up
	close(blockingJob)
}

func TestClosePreventsFutureSubmits(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Close the queue
	err = queue.Close(ctx)
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Try to submit after close - should fail
	successJob := &successJob{processed: &atomic.Bool{}}
	err = queue.Submit(context.Background(), "test-job", successJob)
	if err == nil {
		t.Error("expected error when submitting to closed queue")
	}
}

func TestRetryCount(t *testing.T) {
	maxRetries := 2
	queue, err := NewQueue(
		WithMaxRetries(maxRetries),
		WithRetryDelay(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	failingJob := &failJob{attempts: &atomic.Int32{}}
	ctx := context.Background()

	err = queue.Submit(ctx, "test-job", failingJob)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait for retries to complete
	time.Sleep(500 * time.Millisecond)

	// The job should have been attempted initial + maxRetries times
	attempts := failingJob.attempts.Load()
	// At minimum 1 initial attempt + retries
	if attempts < int32(1+maxRetries) {
		t.Errorf("expected at least %d attempts, got %d", 1+maxRetries, attempts)
	}
}

func TestPanicInJobDoesNotCrashProcess(t *testing.T) {
	queue, err := NewQueue(
		WithWorkerCount(1),
		WithMaxRetries(0),
		WithRetryDelay(0),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	panicJob := &panicJob{}
	ctx := context.Background()

	// This should not panic
	err = queue.Submit(ctx, "panic-job", panicJob)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait for job to be processed with no retries
	time.Sleep(300 * time.Millisecond)

	// Check that stats recorded the failure
	stats := queue.Stats()
	if stats.Failed == 0 {
		t.Errorf("expected failed count > 0 after panic, got stats: %+v", stats)
	}
}

// blockingJobImpl is a job that blocks until signaled
type blockingJobImpl struct {
	blockChan chan struct{}
}

func (j *blockingJobImpl) Process(ctx context.Context) error {
	<-j.blockChan
	return nil
}
