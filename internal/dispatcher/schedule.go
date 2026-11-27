package dispatcher

import (
	"container/heap"
	"time"

	"github.com/zt4ff/gokue/v2/config"
	"github.com/zt4ff/gokue/v2/internal/logger"
)

// taskHeap orders scheduled tasks by RunAt, then by submission time.
type taskHeap []Task

func (h taskHeap) Len() int { return len(h) }
func (h taskHeap) Less(i, j int) bool {
	if !h[i].RunAt.Equal(h[j].RunAt) {
		return h[i].RunAt.Before(h[j].RunAt)
	}
	return h[i].SubmittedAt.Before(h[j].SubmittedAt)
}
func (h taskHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *taskHeap) Push(x any)   { *h = append(*h, x.(Task)) }
func (h *taskHeap) Pop() any {
	old := *h
	t := old[len(old)-1]
	old[len(old)-1] = Task{}
	*h = old[:len(old)-1]
	return t
}

// scheduler holds delayed tasks and retries waiting for their backoff, and
// hands each one to a free worker once its RunAt has passed. On quit it drops
// everything it still holds.
func (d *Dispatcher) scheduler() {
	defer d.wg.Done()
	var h taskHeap
	for {
		var due chan Task
		var next Task
		var wake <-chan time.Time
		var timer *time.Timer
		if len(h) > 0 {
			if wait := time.Until(h[0].RunAt); wait <= 0 {
				due, next = d.due, h[0]
			} else {
				timer = time.NewTimer(wait)
				wake = timer.C
			}
		}

		select {
		case t := <-d.schedule:
			heap.Push(&h, t)
		case due <- next:
			heap.Pop(&h)
			d.scheduled.Add(-1)
		case <-wake:
		case <-d.quit:
			if timer != nil {
				timer.Stop()
			}
			for _, t := range h {
				d.scheduled.Add(-1)
				d.abandon(t)
			}
			d.dropQueued()
			return
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// abandon gives up on a task during shutdown. A retry counts as failed, as
// its earlier attempt already ran; a job that never started counts as dropped.
func (d *Dispatcher) abandon(t Task) {
	d.releaseSlot(&t)
	if t.attempt == 0 {
		d.collector.IncDropped()
		logger.LogJobAbandoned(d.logger, t.Name, 0, logger.ReasonDispatcherShutdown, 0)
	} else {
		d.collector.IncFailed()
		duration := time.Since(t.firstStart)
		logger.LogJobAbandoned(d.logger, t.Name, t.attempt, logger.ReasonDispatcherShutdown, duration)
		d.callHook(d.cfg.OnFailure, config.JobEvent{Name: t.Name, Attempt: t.attempt, Err: t.lastErr, Duration: duration})
	}
	d.work.Done()
}

// dropQueued abandons tasks still waiting in the tasks channel.
func (d *Dispatcher) dropQueued() {
	for {
		select {
		case t := <-d.tasks:
			d.abandon(t)
		default:
			return
		}
	}
}

// releaseSlot frees the queue slot t holds, if any.
func (d *Dispatcher) releaseSlot(t *Task) {
	if t.holdsSlot {
		<-d.slots
		t.holdsSlot = false
	}
}
