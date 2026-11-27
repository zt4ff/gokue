package dispatcher_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zt4ff/gokue/v2/backend"
	"github.com/zt4ff/gokue/v2/config"
	"github.com/zt4ff/gokue/v2/internal/dispatcher"
	"github.com/zt4ff/gokue/v2/stats"
)

func ptr[T any](v T) *T { return &v }

func TestRetryBackoffDoesNotHoldWorker(t *testing.T) {
	cfg := regressionConfig(1, 10)
	cfg.MaxRetries = 1
	cfg.RetryDelay = time.Hour
	cfg.BackoffStrategy = config.Constant
	d := dispatcher.NewDispatcher(cfg, stats.NewCollector())

	failing := jobFunc(func(context.Context) error { return errors.New("fail") })
	if err := d.Submit(context.Background(), dispatcher.Task{Name: "failing", Job: failing}); err != nil {
		t.Fatal(err)
	}
	mustWait(t, func() bool { return d.Stats().Scheduled == 1 })

	var ran atomic.Int32
	if err := d.Submit(context.Background(), dispatcher.Task{Name: "next", Job: &countingJob{ran: &ran}}); err != nil {
		t.Fatal(err)
	}
	mustWait(t, func() bool { return ran.Load() == 1 })
	if s := d.Stats(); s.Running != 0 || s.Scheduled != 1 || s.IdleWorkers != 1 {
		t.Fatalf("gauges during backoff = %+v, want running 0, scheduled 1, idle 1", s)
	}

	if err := d.CloseWithMode(context.Background(), dispatcher.ImmediateMode); err != nil {
		t.Fatal(err)
	}
	mustWait(t, func() bool { return d.Stats().Failed == 1 })
	if s := d.Stats(); s.Scheduled != 0 || s.Processed != 1 {
		t.Fatalf("stats after Close = %+v", s)
	}
}

func TestDelayedTaskWaitsForRunAt(t *testing.T) {
	d := dispatcher.NewDispatcher(regressionConfig(2, 10), stats.NewCollector())
	defer d.Close(context.Background())

	start := time.Now()
	started := make(chan time.Time, 2)
	j := jobFunc(func(context.Context) error { started <- time.Now(); return nil })
	if err := d.Submit(context.Background(), dispatcher.Task{Job: j, Delay: ptr(150 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	if err := d.Submit(context.Background(), dispatcher.Task{Job: j, RunAt: start.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := <-started; got.Sub(start) > 100*time.Millisecond {
		t.Fatalf("past RunAt started after %v", got.Sub(start))
	}
	if got := <-started; got.Sub(start) < 150*time.Millisecond {
		t.Fatalf("delayed task started after %v, want >= 150ms", got.Sub(start))
	}
}

func TestDelayedTasksRunInRunAtOrder(t *testing.T) {
	d := dispatcher.NewDispatcher(regressionConfig(1, 10), stats.NewCollector())
	defer d.Close(context.Background())

	order := make(chan string, 3)
	now := time.Now()
	for _, c := range []struct {
		name string
		at   time.Duration
	}{{"c", 120 * time.Millisecond}, {"a", 40 * time.Millisecond}, {"b", 80 * time.Millisecond}} {
		name := c.name
		j := jobFunc(func(context.Context) error { order <- name; return nil })
		if err := d.Submit(context.Background(), dispatcher.Task{Name: name, Job: j, RunAt: now.Add(c.at)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"a", "b", "c"} {
		if got := <-order; got != want {
			t.Fatalf("ran %s, want %s", got, want)
		}
	}
}

func TestDelayedTaskTakesQueueSlot(t *testing.T) {
	d := dispatcher.NewDispatcher(regressionConfig(1, 1), stats.NewCollector())
	defer d.CloseWithMode(context.Background(), dispatcher.ImmediateMode)

	var ran atomic.Int32
	if err := d.TrySubmit(context.Background(), dispatcher.Task{Job: &countingJob{ran: &ran}, Delay: ptr(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := d.Backlog(); got != 1 {
		t.Fatalf("Backlog = %d, want 1", got)
	}
	err := d.TrySubmit(context.Background(), dispatcher.Task{Job: &countingJob{ran: &ran}})
	if !errors.Is(err, dispatcher.ErrQueueFull) {
		t.Fatalf("TrySubmit = %v, want ErrQueueFull", err)
	}
}

func TestNegativeDelayRejected(t *testing.T) {
	d := dispatcher.NewDispatcher(regressionConfig(1, 1), stats.NewCollector())
	defer d.Close(context.Background())
	err := d.Submit(context.Background(), dispatcher.Task{Job: &countingJob{ran: &atomic.Int32{}}, Delay: ptr(-time.Second)})
	if !errors.Is(err, dispatcher.ErrInvalidTask) {
		t.Fatalf("Submit = %v, want ErrInvalidTask", err)
	}
}

func TestDrainWaitsForDelayedTask(t *testing.T) {
	d := dispatcher.NewDispatcher(regressionConfig(1, 10), stats.NewCollector())
	var ran atomic.Int32
	if err := d.Submit(context.Background(), dispatcher.Task{Job: &countingJob{ran: &ran}, Delay: ptr(100 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 1 {
		t.Fatal("Close returned before the delayed task ran")
	}
}

func TestCloseTimeoutDropsDelayedTask(t *testing.T) {
	d := dispatcher.NewDispatcher(regressionConfig(1, 10), stats.NewCollector())
	var ran atomic.Int32
	if err := d.Submit(context.Background(), dispatcher.Task{Job: &countingJob{ran: &ran}, Delay: ptr(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := d.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want deadline exceeded", err)
	}
	mustWait(t, func() bool { return d.Stats().Dropped == 1 })
	if s := d.Stats(); ran.Load() != 0 || s.Scheduled != 0 || s.Failed != 0 {
		t.Fatalf("after Close: ran %d, stats %+v", ran.Load(), s)
	}
}

func TestStoreDelayedJobNotClaimedEarly(t *testing.T) {
	store := backend.NewMemoryStore()
	d := dispatcher.NewDispatcher(storeConfig(t, store), stats.NewCollector())
	defer d.Close(context.Background())

	j := behave(t.Name(), func(context.Context) error { return nil })
	if err := d.Submit(context.Background(), dispatcher.Task{Job: j, Delay: ptr(150 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	rec := onlyRecord(t, store)
	if rec.RunAt == nil || time.Until(*rec.RunAt) <= 0 {
		t.Fatalf("saved RunAt = %v, want in the future", rec.RunAt)
	}
	time.Sleep(50 * time.Millisecond)
	if runsOf(t.Name()) != 0 {
		t.Fatal("delayed job ran early")
	}
	mustWait(t, func() bool { return statusCount(t, store, backend.StatusSucceeded) == 1 })
}

func TestStoreRetryIsSavedAndSurvivesRestart(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)
	cfg.WorkerCount = 1
	cfg.MaxRetries = 1
	cfg.RetryDelay = 200 * time.Millisecond
	cfg.BackoffStrategy = config.Constant
	first := dispatcher.NewDispatcher(cfg, stats.NewCollector())

	var calls atomic.Int32
	j := behave(t.Name(), func(context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("first attempt fails")
		}
		return nil
	})
	if err := first.Submit(context.Background(), dispatcher.Task{Job: j}); err != nil {
		t.Fatal(err)
	}
	mustWait(t, func() bool { r := onlyRecord(t, store); return r.Status == backend.StatusPending && r.RunAt != nil })
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec := onlyRecord(t, store); rec.Attempts != 1 || rec.LastError != "first attempt fails" {
		t.Fatalf("pending retry = %+v", rec)
	}

	second := dispatcher.NewDispatcher(cfg, stats.NewCollector())
	defer second.Close(context.Background())
	mustWait(t, func() bool { return statusCount(t, store, backend.StatusSucceeded) == 1 })
	if rec := onlyRecord(t, store); rec.Attempts != 2 {
		t.Fatalf("record = %+v, want 2 attempts", rec)
	}
}
