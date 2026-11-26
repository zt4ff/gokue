package dispatcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zt4ff/gokue/v2/backend"
	"github.com/zt4ff/gokue/v2/internal/logger"
	"github.com/zt4ff/gokue/v2/job"
)

// storeOpTimeout bounds each store call made by workers and the reaper.
const storeOpTimeout = 10 * time.Second

// localTask is a per-submit override kept for a job saved by this process.
type localTask struct {
	task    Task
	savedAt time.Time
}

func (t Task) hasOverrides() bool {
	return t.MaxRetries != nil || t.RetryDelay != nil || t.RetryPredicates != nil
}

func newJobID() (job.ID, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return job.ID(hex.EncodeToString(b[:])), nil
}

// save persists task as a pending record and wakes an idle worker.
// The caller holds submitMu.
func (d *Dispatcher) save(ctx context.Context, task Task) error {
	select {
	case <-d.closing:
		logger.LogSubmitRejected(d.logger, task.Name, logger.ReasonDispatcherClosed)
		return ErrClosed
	default:
	}
	name, payload, err := d.codec.Encode(task.Job)
	if err != nil {
		return err
	}
	id, err := newJobID()
	if err != nil {
		return err
	}
	if task.hasOverrides() {
		d.local.Store(id, localTask{task: task, savedAt: time.Now()})
	}
	rec := backend.JobRecord{
		ID:        id,
		Name:      name,
		Payload:   payload,
		Status:    backend.StatusPending,
		CreatedAt: task.SubmittedAt,
	}
	if err := d.store.Save(ctx, rec); err != nil {
		d.local.Delete(id)
		return err
	}
	d.collector.IncEnqueued()
	logger.LogSubmitEnqueued(d.logger, task.Name)
	d.wakeWorker()
	return nil
}

func (d *Dispatcher) wakeWorker() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) logStoreError(op string, err error) {
	if d.logger == nil || err == nil {
		return
	}
	d.logger.Warn("store_error", "op", op, "error", err.Error())
}

// storeWorker claims jobs from the store and runs them until Close is called.
// Unclaimed records stay in the store.
func (d *Dispatcher) storeWorker() {
	defer d.wg.Done()
	defer d.liveWorkers.Add(-1)

	for {
		select {
		case <-d.closing:
			return
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		rec, err := d.store.Claim(ctx)
		cancel()
		if err != nil {
			if !errors.Is(err, backend.ErrNotFound) {
				d.logStoreError("claim", err)
			}
			timer := time.NewTimer(d.cfg.PollInterval)
			select {
			case <-d.wake:
			case <-timer.C:
			case <-d.closing:
				timer.Stop()
				return
			}
			timer.Stop()
			continue
		}

		d.running.Add(1)
		d.runRecord(rec)
		d.running.Add(-1)
	}
}

// claimed is a record owned by one worker. The heartbeat and the final write
// share it, so access goes through mu.
type claimed struct {
	mu   sync.Mutex
	rec  backend.JobRecord
	lost bool
}

// write applies change to the record and saves it. It reports false once the
// record has been taken over by another worker.
func (d *Dispatcher) write(c *claimed, op string, change func(*backend.JobRecord)) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lost {
		return false
	}
	next := c.rec
	change(&next)

	ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
	defer cancel()
	err := d.store.Update(ctx, next)
	switch {
	case err == nil:
		next.Version++
		c.rec = next
		return true
	case errors.Is(err, backend.ErrConflict), errors.Is(err, backend.ErrNotFound):
		c.lost = true
		d.logStoreError(op, err)
		return false
	default:
		// Transient failure: keep ownership and let the next write retry.
		d.logStoreError(op, err)
		return true
	}
}

// heartbeat renews the record's lease until stop is closed or the lease is lost.
func (d *Dispatcher) heartbeat(c *claimed, stop <-chan struct{}) {
	interval := d.cfg.Lease / 3
	if interval <= 0 {
		interval = time.Nanosecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		ok := d.write(c, "heartbeat", func(rec *backend.JobRecord) {
			now := time.Now().UTC()
			rec.StartedAt = &now
		})
		if !ok {
			return
		}
	}
}

// runRecord decodes and runs a claimed record, then saves its outcome.
func (d *Dispatcher) runRecord(rec backend.JobRecord) {
	c := &claimed{rec: rec}
	finish := func(status backend.JobStatus, jobErr error) {
		d.write(c, "finish", func(r *backend.JobRecord) {
			now := time.Now().UTC()
			r.Status = status
			r.FinishedAt = &now
			r.LastError = ""
			if jobErr != nil {
				r.LastError = jobErr.Error()
			}
		})
	}
	release := func() {
		d.write(c, "release", func(r *backend.JobRecord) {
			r.Status = backend.StatusPending
			r.StartedAt = nil
		})
	}

	// Claimed while shutting down immediately: give it back untouched.
	select {
	case <-d.quit:
		release()
		return
	default:
	}

	task := Task{Name: string(rec.Name), SubmittedAt: rec.CreatedAt, ctx: context.Background()}
	j, err := d.codec.Decode(rec.Name, rec.Payload)
	if err != nil {
		d.local.Delete(rec.ID)
		d.collector.IncFailed()
		logger.LogJobFailure(d.logger, task.Name, int(rec.Attempts), err, 0)
		finish(backend.StatusFailed, err)
		return
	}
	task.Job = j
	// Retry overrides apply only in the process that submitted the job.
	if v, ok := d.local.LoadAndDelete(rec.ID); ok {
		o := v.(localTask).task
		task.MaxRetries, task.RetryDelay, task.RetryPredicates = o.MaxRetries, o.RetryDelay, o.RetryPredicates
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.heartbeat(c, stop)
	}()
	result, jobErr := d.safeExecute(task)
	close(stop)
	<-done

	switch result {
	case outcomeSucceeded:
		finish(backend.StatusSucceeded, nil)
	case outcomeAbandoned:
		release()
	default:
		finish(backend.StatusFailed, jobErr)
	}
}

// reaper returns records whose lease expired (their worker died) to pending,
// once at start and then every Lease/2 until Close is called.
func (d *Dispatcher) reaper() {
	interval := d.cfg.Lease / 2
	if interval <= 0 {
		interval = time.Nanosecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		n, err := d.store.RequeueStale(ctx, d.cfg.Lease)
		cancel()
		if err != nil {
			d.logStoreError("requeue_stale", err)
		}
		for ; n > 0; n-- {
			d.wakeWorker()
		}
		d.pruneLocal()

		select {
		case <-d.closing:
			return
		case <-ticker.C:
		}
	}
}

// pruneLocal forgets overrides of jobs that another process picked up.
func (d *Dispatcher) pruneLocal() {
	cutoff := time.Now().Add(-d.cfg.Lease)
	d.local.Range(func(key, value any) bool {
		if value.(localTask).savedAt.After(cutoff) {
			return true
		}
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		rec, err := d.store.Get(ctx, key.(job.ID))
		cancel()
		if errors.Is(err, backend.ErrNotFound) || (err == nil && rec.Status != backend.StatusPending) {
			d.local.Delete(key)
		}
		return true
	})
}

// DeadJobs lists up to limit failed records, oldest first. limit <= 0 means
// no limit.
func (d *Dispatcher) DeadJobs(ctx context.Context, limit int64) ([]backend.JobRecord, error) {
	if d.store == nil {
		return nil, ErrNotPersistent
	}
	status := backend.StatusFailed
	return d.store.List(ctx, backend.ListFilter{Status: &status, Limit: limit})
}

// Replay returns the failed record id to pending with its attempt count reset,
// so a worker runs it again with a full set of retries.
func (d *Dispatcher) Replay(ctx context.Context, id job.ID) error {
	if d.store == nil {
		return ErrNotPersistent
	}
	rec, err := d.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if rec.Status != backend.StatusFailed {
		return fmt.Errorf("%w: job %s is %s", ErrNotFailed, id, rec.Status)
	}
	rec.Status = backend.StatusPending
	rec.Attempts = 0
	rec.StartedAt = nil
	rec.FinishedAt = nil
	rec.LastError = ""
	if err := d.store.Update(ctx, rec); err != nil {
		return err
	}
	d.wakeWorker()
	return nil
}
