package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zt4ff/gokue/config"
	"github.com/zt4ff/gokue/job"
	"github.com/zt4ff/gokue/stats"
)

var (
	ErrClosed    = errors.New("dispatcher is closed")
	ErrQueueFull = errors.New("dispatcher queue is full")
	ErrNilJob    = errors.New("job cannot be nil")
	ErrNilCtx    = errors.New("context cannot be nil")
)

type Task struct {
	Name        string
	Job         job.Job
	SubmittedAt time.Time
}

type Dispatcher struct {
	cfg       config.Config
	collector *stats.Collector
	tasks     chan Task

	submitMu sync.RWMutex
	closed   bool
	wg       sync.WaitGroup
}

func New(cfg config.Config, collector *stats.Collector) *Dispatcher {
	if collector == nil {
		collector = stats.NewCollector()
	}

	d := &Dispatcher{
		cfg:       cfg,
		collector: collector,
		tasks:     make(chan Task, cfg.QueueSize),
	}

	for worker := 0; worker < cfg.WorkerCount; worker++ {
		d.wg.Add(1)
		go d.worker()
	}

	return d
}

func (d *Dispatcher) Submit(ctx context.Context, task Task) error {
	if ctx == nil {
		return ErrNilCtx
	}
	if task.Job == nil {
		return ErrNilJob
	}
	if task.SubmittedAt.IsZero() {
		task.SubmittedAt = time.Now().UTC()
	}

	d.submitMu.RLock()
	defer d.submitMu.RUnlock()

	if d.closed {
		return ErrClosed
	}

	select {
	case d.tasks <- task:
		d.collector.IncEnqueued()
		return nil
	case <-ctx.Done():
		d.collector.IncDropped()
		return ctx.Err()
	}
}

func (d *Dispatcher) TrySubmit(ctx context.Context, task Task) error {
	if ctx == nil {
		return ErrNilCtx
	}
	if task.Job == nil {
		return ErrNilJob
	}
	if task.SubmittedAt.IsZero() {
		task.SubmittedAt = time.Now().UTC()
	}

	d.submitMu.RLock()
	defer d.submitMu.RUnlock()

	if d.closed {
		return ErrClosed
	}

	select {
	case d.tasks <- task:
		d.collector.IncEnqueued()
		return nil
	default:
		d.collector.IncDropped()
		return ErrQueueFull
	}
}

func (d *Dispatcher) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrNilCtx
	}

	d.submitMu.Lock()
	if !d.closed {
		d.closed = true
		close(d.tasks)
	}
	d.submitMu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Dispatcher) Stats() stats.Snapshot {
	return d.collector.Snapshot()
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()

	for task := range d.tasks {
		d.execute(task)
	}
}

func (d *Dispatcher) execute(task Task) {
	var err error
	for attempt := 0; attempt <= d.cfg.MaxRetries; attempt++ {
		err = runJob(task.Job, d.cfg.JobTimeout)
		if err == nil {
			d.collector.IncProcessed()
			return
		}

		if attempt < d.cfg.MaxRetries {
			d.collector.IncRetried()
			if delay := retryDelay(d.cfg.RetryDelay, attempt); delay > 0 {
				time.Sleep(delay)
			}
		}
	}

	d.collector.IncFailed()
}

func runJob(task job.Job, timeout time.Duration) (err error) {
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job panic: %v", recovered)
		}
	}()

	return task.Process(ctx)
}

func retryDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		return 0
	}
	return base * time.Duration(attempt+1)
}
