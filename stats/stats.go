package stats

import "sync/atomic"

// Collector tracks queue activity without taking locks on the hot path.
type Collector struct {
	enqueued  atomic.Uint64
	processed atomic.Uint64
	failed    atomic.Uint64
	retried   atomic.Uint64
	dropped   atomic.Uint64
}

type Snapshot struct {
	Enqueued  uint64
	Processed uint64
	Failed    uint64
	Retried   uint64
	Dropped   uint64
}

func NewCollector() *Collector {
	return &Collector{}
}

func (c *Collector) IncEnqueued() {
	c.enqueued.Add(1)
}

func (c *Collector) IncProcessed() {
	c.processed.Add(1)
}

func (c *Collector) IncFailed() {
	c.failed.Add(1)
}

func (c *Collector) IncRetried() {
	c.retried.Add(1)
}

func (c *Collector) IncDropped() {
	c.dropped.Add(1)
}

func (c *Collector) Snapshot() Snapshot {
	return Snapshot{
		Enqueued:  c.enqueued.Load(),
		Processed: c.processed.Load(),
		Failed:    c.failed.Load(),
		Retried:   c.retried.Load(),
		Dropped:   c.dropped.Load(),
	}
}
