package gokue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zt4ff/gokue/logging"
	"github.com/zt4ff/gokue/stats"
)

// testLogger records log messages for verification.
type testLogger struct {
	mu  sync.Mutex
	msg []string
}

func (l *testLogger) Log(level logging.Level, message string, fields ...interface{}) {
	l.mu.Lock()
	l.msg = append(l.msg, message)
	l.mu.Unlock()
}

func (l *testLogger) Debug(message string, fields ...interface{}) {
	l.Log(logging.LevelDebug, message, fields...)
}

func (l *testLogger) Info(message string, fields ...interface{}) {
	l.Log(logging.LevelInfo, message, fields...)
}

func (l *testLogger) Warn(message string, fields ...interface{}) {
	l.Log(logging.LevelWarn, message, fields...)
}

func (l *testLogger) Error(message string, fields ...interface{}) {
	l.Log(logging.LevelError, message, fields...)
}

func (l *testLogger) has(message string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range l.msg {
		if m == message {
			return true
		}
	}
	return false
}

func TestMaxRetriesOptionDisablesRetries(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	failingJob := &failJob{attempts: &atomic.Int32{}}
	if err := queue.Submit(context.Background(), failingJob, MaxRetries(0)); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if attempts := failingJob.attempts.Load(); attempts != 1 {
		t.Errorf("expected 1 attempt with MaxRetries(0), got %d", attempts)
	}
}

func TestMaxRetriesOptionLimitsRetries(t *testing.T) {
	queue, err := NewQueue(WithRetryDelay(5 * time.Millisecond))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	failingJob := &failJob{attempts: &atomic.Int32{}}
	if err := queue.Submit(context.Background(), failingJob, MaxRetries(2)); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	if attempts := failingJob.attempts.Load(); attempts != 3 {
		t.Errorf("expected 3 attempts with MaxRetries(2), got %d", attempts)
	}
}

func TestRetryDelayOptionOverridesGlobal(t *testing.T) {
	queue, err := NewQueue(WithRetryDelay(30 * time.Second))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	failingJob := &failJob{attempts: &atomic.Int32{}}
	start := time.Now()
	if err := queue.Submit(context.Background(), failingJob, RetryDelay(10*time.Millisecond), MaxRetries(1)); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	for {
		if failingJob.attempts.Load() >= 2 {
			break
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("timed out waiting for retry")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("expected quick retry via RetryDelay option, took %v", elapsed)
	}
}

func TestWithLoggerOption(t *testing.T) {
	logger := &testLogger{}
	var target logging.Logger = &logging.NoOpLogger{}

	WithLogger(logger)(&target)

	if _, ok := target.(*testLogger); !ok {
		t.Errorf("expected WithLogger to set the provided logger, got %T", target)
	}
}

func TestNilQueueReceiver(t *testing.T) {
	var q *Queue

	if err := q.Submit(context.Background(), &successJob{}); err == nil || err.Error() != "queue is nil" {
		t.Errorf("expected 'queue is nil' error, got %v", err)
	}
	if err := q.TrySubmit(context.Background(), &successJob{}); err == nil || err.Error() != "queue is nil" {
		t.Errorf("expected 'queue is nil' error, got %v", err)
	}
	if err := q.Close(context.Background()); err == nil || err.Error() != "queue is nil" {
		t.Errorf("expected 'queue is nil' error, got %v", err)
	}
	q.Run(&successJob{})
	if s := q.Stats(); s.Enqueued != 0 || s.Processed != 0 {
		t.Errorf("expected empty stats for nil queue, got %+v", s)
	}
}

func TestNilCollectorStats(t *testing.T) {
	q := &Queue{}
	if s := q.Stats(); s.Enqueued != 0 || s.Processed != 0 {
		t.Errorf("expected empty stats for queue without collector, got %+v", s)
	}
}

func TestSubmitNilOptions(t *testing.T) {
	queue, err := NewQueue(WithQueueSize(10), WithWorkerCount(1))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	job := &successJob{processed: &atomic.Bool{}}
	if err := queue.Submit(context.Background(), job, nil, MaxRetries(0)); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if !job.processed.Load() {
		t.Error("expected job to be processed")
	}
}

func TestTrySubmitNilTask(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	if err := queue.TrySubmit(context.Background(), nil); err == nil || err.Error() != "job cannot be nil" {
		t.Errorf("expected 'job cannot be nil' error, got %v", err)
	}
}

func TestTrySubmitNilContext(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	//lint:ignore SA1012 Intentionally passing a nil context to verify validation.
	err = queue.TrySubmit(nil, &successJob{})
	if err == nil || err.Error() != "context cannot be nil" {
		t.Errorf("expected 'context cannot be nil' error, got %v", err)
	}
}

func TestTrySubmitSucceeds(t *testing.T) {
	queue, err := NewQueue(WithQueueSize(10), WithWorkerCount(1))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	job := &successJob{processed: &atomic.Bool{}}
	if err := queue.TrySubmit(context.Background(), job); err != nil {
		t.Fatalf("try submit failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if !job.processed.Load() {
		t.Error("expected job to be processed")
	}
}

func TestTrySubmitWithOptions(t *testing.T) {
	queue, err := NewQueue(WithQueueSize(10), WithWorkerCount(1))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	failingJob := &failJob{attempts: &atomic.Int32{}}
	if err := queue.TrySubmit(context.Background(), failingJob, nil, MaxRetries(0)); err != nil {
		t.Fatalf("try submit failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if attempts := failingJob.attempts.Load(); attempts != 1 {
		t.Errorf("expected 1 attempt with MaxRetries(0), got %d", attempts)
	}
}

func TestCloseNilContext(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	//lint:ignore SA1012 Intentionally passing a nil context to verify validation.
	err = queue.Close(nil)
	if err == nil || err.Error() != "context cannot be nil" {
		t.Errorf("expected 'context cannot be nil' error, got %v", err)
	}
	queue.Close(context.Background())
}

func TestRunIgnoredError(t *testing.T) {
	queue, err := NewQueue(WithQueueSize(1), WithWorkerCount(1))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	// Run on a job that fails should not panic and should increment failed stats.
	failingJob := &failJob{attempts: &atomic.Int32{}}
	queue.Run(failingJob, MaxRetries(0))

	time.Sleep(100 * time.Millisecond)

	if stats := queue.Stats(); stats.Failed == 0 {
		t.Error("expected failed count > 0")
	}
}

func TestNewQueueWithLoggerEmitsEvents(t *testing.T) {
	logger := &testLogger{}
	queue, err := NewQueueWithLogger(logger, WithWorkerCount(1))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	job := &successJob{processed: &atomic.Bool{}}
	if err := queue.Submit(context.Background(), job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if err := queue.Close(context.Background()); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	if !logger.has(string(logging.EventSubmitEnqueued)) {
		t.Error("expected submit enqueued event to be logged")
	}
	if !logger.has(string(logging.EventCloseCompleted)) {
		t.Error("expected close completed event to be logged")
	}
}

func TestNewQueueNilOption(t *testing.T) {
	queue, err := NewQueue(nil)
	if err != nil {
		t.Fatalf("expected no error for nil option, got %v", err)
	}
	defer queue.Close(context.Background())

	job := &successJob{processed: &atomic.Bool{}}
	if err := queue.Submit(context.Background(), job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
}

func TestSubmitErrorsPropagate(t *testing.T) {
	queue, err := NewQueue(WithQueueSize(1), WithWorkerCount(1))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	blockingJob := make(chan struct{})
	defer close(blockingJob)

	if err := queue.Submit(context.Background(), &blockingJobImpl{blockChan: blockingJob}); err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	if err := queue.Submit(context.Background(), &blockingJobImpl{blockChan: blockingJob}); err != nil {
		t.Fatalf("second submit failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = queue.Submit(ctx, &successJob{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestQueueStatsByJobName(t *testing.T) {
	queue, err := NewQueue(
		WithWorkerCount(1),
		WithMaxRetries(0),
		WithLatencyHistogram(time.Millisecond, 10*time.Millisecond, 100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	if err := queue.Submit(context.Background(), &namedJob{processed: &atomic.Bool{}}); err != nil {
		t.Fatalf("submit named job failed: %v", err)
	}
	failing := &failJob{attempts: &atomic.Int32{}}
	if err := queue.Submit(context.Background(), failing, MaxRetries(0)); err != nil {
		t.Fatalf("submit failing job failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	jobs := queue.StatsByJobName()
	byName := map[string]stats.JobStat{}
	for _, j := range jobs {
		byName[j.Name] = j
	}

	named, ok := byName["custom-named-job"]
	if !ok {
		t.Fatalf("expected per-job stats for custom-named-job, got %v", jobs)
	}
	if named.Enqueued != 1 || named.Processed != 1 {
		t.Errorf("unexpected named job stats: %+v", named)
	}
	if named.SuccessHistogram == nil || named.SuccessHistogram.Count != 1 {
		t.Errorf("expected success histogram with 1 observation, got %+v", named.SuccessHistogram)
	}

	// failJob's name is derived from its type name.
	if failed, ok := byName["failJob"]; !ok || failed.Failed != 1 {
		t.Errorf("expected failJob per-job stats with Failed 1, got %v", byName["failJob"])
	}

	// Aggregate remains available and consistent.
	agg := queue.Stats()
	if agg.Enqueued != 2 || agg.Processed != 1 || agg.Failed != 1 {
		t.Errorf("unexpected aggregate stats: %+v", agg)
	}
}

func TestNilQueueStatsByJobName(t *testing.T) {
	var q *Queue
	if jobs := q.StatsByJobName(); jobs != nil {
		t.Errorf("expected nil per-job stats for nil queue, got %v", jobs)
	}
}
