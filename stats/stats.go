// Package stats provides statistics collection for the gokue job queue.
package stats

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// UntrackedBucket is the name under which counters for job names that exceed
// the configured cardinality limit are aggregated.
const UntrackedBucket = "untracked"

// Snapshot represents a point-in-time capture of queue statistics.
type Snapshot struct {
	// Enqueued is the total number of jobs enqueued.
	Enqueued uint64
	// Processed is the total number of jobs successfully processed.
	Processed uint64
	// Failed is the total number of jobs that failed after all retries.
	Failed uint64
	// Retried is the total number of job retry attempts.
	Retried uint64
	// Dropped is the total number of jobs dropped.
	Dropped uint64
	// RetryPredicatesFailed is the total number of job that failed a retry predicate function.
	RetryPredicatesFailed uint64
}

// LatencySummary summarizes observed latencies for a job name.
// Avg is the mean latency (Sum / Count); it is 0 when Count is 0.
type LatencySummary struct {
	// Count is the number of observed latencies.
	Count uint64
	// Sum is the total observed latency.
	Sum time.Duration
	// Min is the smallest observed latency.
	Min time.Duration
	// Max is the largest observed latency.
	Max time.Duration
	// Avg is the mean observed latency.
	Avg time.Duration
}

// Histogram is a cumulative latency histogram with strictly increasing bucket
// bounds. Counts[i] is the number of observations less than or equal to
// Bounds[i]; observations above the last bound are counted only in Count.
type Histogram struct {
	// Bounds are the inclusive upper bounds of each bucket.
	Bounds []time.Duration
	// Counts are the cumulative bucket counts (same length as Bounds).
	Counts []uint64
	// Count is the total number of observations recorded.
	Count uint64
}

// Quantile returns an estimate of the q-th latency percentile (0 <= q <= 1).
// Observations that fell in the final open-ended bucket are estimated as the
// last bound. It returns 0 when there are no observations or q is out of range.
func (h *Histogram) Quantile(q float64) time.Duration {
	if h == nil || h.Count == 0 || q < 0 || q > 1 {
		return 0
	}
	target := q * float64(h.Count)
	for i, count := range h.Counts {
		if float64(count) >= target {
			return h.Bounds[i]
		}
	}
	return h.Bounds[len(h.Bounds)-1]
}

// JobStat is the per-job-name breakdown of queue statistics.
type JobStat struct {
	// Name is the job name these statistics describe.
	Name string
	// Enqueued is the number of jobs of this name enqueued.
	Enqueued uint64
	// Processed is the number of jobs of this name successfully processed.
	Processed uint64
	// Failed is the number of jobs of this name that failed after all retries.
	Failed uint64
	// Retried is the number of retry attempts for jobs of this name.
	Retried uint64
	// Dropped is the number of jobs of this name dropped (queue full or cancelled).
	Dropped uint64
	// RetryPredicatesFailed is the number of jobs of this name that failed a
	// retry predicate function.
	RetryPredicatesFailed uint64
	// SuccessLatency summarizes the execution latency of successful jobs.
	SuccessLatency LatencySummary
	// FailureLatency summarizes the execution latency of failed jobs.
	FailureLatency LatencySummary
	// SuccessHistogram is the latency histogram of successful jobs. It is nil
	// when histogram collection is disabled.
	SuccessHistogram *Histogram
	// FailureHistogram is the latency histogram of failed jobs. It is nil when
	// histogram collection is disabled.
	FailureHistogram *Histogram
}

func (s JobStat) isZero() bool {
	return s.Enqueued == 0 && s.Processed == 0 && s.Failed == 0 &&
		s.Retried == 0 && s.Dropped == 0 && s.RetryPredicatesFailed == 0
}

// Collector tracks queue activity using atomic operations on the hot path.
// Aggregate counters are always maintained; per-job-name counters are tracked
// alongside them. Per-name snapshots are weakly consistent: counters are read
// individually at atomic points in time, so a snapshot may observe slightly
// different instants across names.
type Collector struct {
	// enqueued counts the number of jobs enqueued.
	enqueued atomic.Uint64
	// processed counts the number of jobs successfully processed.
	processed atomic.Uint64
	// failed counts the number of jobs that failed after all retries.
	failed atomic.Uint64
	// retried counts the number of job retry attempts.
	retried atomic.Uint64
	// dropped counts the number of jobs dropped (queue full or cancelled).
	dropped atomic.Uint64
	// retryPredicatesFailed counts the number of jobs that failed a retry predicate function.
	retryPredicatesFailed atomic.Uint64

	// jobs holds the per-job-name counters.
	jobs sync.Map
	// tracked is the number of distinct job names stored in jobs.
	tracked atomic.Uint64
	// maxJobStats caps the number of distinct tracked names; 0 means unlimited.
	maxJobStats uint64
	// untracked aggregates counters for names beyond maxJobStats.
	untracked *perJobCounters
	// bounds are the latency histogram bucket bounds (empty disables histograms).
	bounds []time.Duration
	// insertMu serializes the jobs map insert path so tracked stays accurate.
	insertMu sync.Mutex
}

// CollectorOption configures a Collector.
type CollectorOption func(*Collector)

// WithMaxJobStats caps the number of distinct job names tracked for per-job
// statistics. A value of zero or negative means no limit. When the limit is
// reached, additional job names are aggregated under the UntrackedBucket name.
func WithMaxJobStats(max int) CollectorOption {
	return func(c *Collector) {
		if max > 0 {
			c.maxJobStats = uint64(max)
		}
	}
}

// WithLatencyBuckets enables per-job latency histograms with the given bucket
// upper bounds (inclusive). Bounds must be positive and strictly increasing;
// an invalid set panics. Success and failure latencies are histogrammed
// separately. Passing no bounds disables histogram collection.
func WithLatencyBuckets(bounds []time.Duration) CollectorOption {
	return func(c *Collector) {
		validateBounds(bounds)
		c.bounds = append([]time.Duration(nil), bounds...)
	}
}

func validateBounds(bounds []time.Duration) {
	for i, b := range bounds {
		if b <= 0 {
			panic("stats: latency histogram bounds must be positive")
		}
		if i > 0 && b <= bounds[i-1] {
			panic("stats: latency histogram bounds must be strictly increasing")
		}
	}
}

// NewCollector creates and returns a new Collector initialized with zero values.
func NewCollector(opts ...CollectorOption) *Collector {
	c := &Collector{}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// perJobCounters holds the per-job-name counters for a single job type.
type perJobCounters struct {
	enqueued              atomic.Uint64
	processed             atomic.Uint64
	failed                atomic.Uint64
	retried               atomic.Uint64
	dropped               atomic.Uint64
	retryPredicatesFailed atomic.Uint64

	successLatencyCount atomic.Uint64
	successLatencySum   atomic.Int64
	successLatencyMin   atomic.Int64
	successLatencyMax   atomic.Int64

	failureLatencyCount atomic.Uint64
	failureLatencySum   atomic.Int64
	failureLatencyMin   atomic.Int64
	failureLatencyMax   atomic.Int64

	bounds           []time.Duration
	successHistogram []atomic.Uint64
	failureHistogram []atomic.Uint64
}

func newPerJobCounters(bounds []time.Duration) *perJobCounters {
	pc := &perJobCounters{bounds: bounds}
	if len(bounds) > 0 {
		pc.successHistogram = make([]atomic.Uint64, len(bounds))
		pc.failureHistogram = make([]atomic.Uint64, len(bounds))
	}
	return pc
}

func (pc *perJobCounters) observeSuccess(latency time.Duration) {
	pc.successLatencyCount.Add(1)
	pc.successLatencySum.Add(int64(latency))
	updateMinMax(&pc.successLatencyMin, &pc.successLatencyMax, latency)
	recordHistogram(pc.successHistogram, pc.bounds, latency)
}

func (pc *perJobCounters) observeFailure(latency time.Duration) {
	pc.failureLatencyCount.Add(1)
	pc.failureLatencySum.Add(int64(latency))
	updateMinMax(&pc.failureLatencyMin, &pc.failureLatencyMax, latency)
	recordHistogram(pc.failureHistogram, pc.bounds, latency)
}

func updateMinMax(min, max *atomic.Int64, latency time.Duration) {
	ns := int64(latency)
	for {
		cur := min.Load()
		if cur != 0 && cur <= ns {
			break
		}
		if min.CompareAndSwap(cur, ns) {
			break
		}
	}
	for {
		cur := max.Load()
		if cur >= ns {
			break
		}
		if max.CompareAndSwap(cur, ns) {
			break
		}
	}
}

func recordHistogram(counts []atomic.Uint64, bounds []time.Duration, latency time.Duration) {
	if len(counts) == 0 {
		return
	}
	i := sort.Search(len(bounds), func(i int) bool {
		return int64(latency) <= int64(bounds[i])
	})
	for ; i < len(counts); i++ {
		counts[i].Add(1)
	}
}

func (pc *perJobCounters) snapshot(name string) JobStat {
	js := JobStat{
		Name:                  name,
		Enqueued:              pc.enqueued.Load(),
		Processed:             pc.processed.Load(),
		Failed:                pc.failed.Load(),
		Retried:               pc.retried.Load(),
		Dropped:               pc.dropped.Load(),
		RetryPredicatesFailed: pc.retryPredicatesFailed.Load(),
		SuccessLatency:        summaryOf(pc.successLatencyCount.Load(), pc.successLatencySum.Load(), pc.successLatencyMin.Load(), pc.successLatencyMax.Load()),
		FailureLatency:        summaryOf(pc.failureLatencyCount.Load(), pc.failureLatencySum.Load(), pc.failureLatencyMin.Load(), pc.failureLatencyMax.Load()),
	}
	if len(pc.successHistogram) > 0 {
		js.SuccessHistogram = histogramOf(pc.bounds, pc.successHistogram, js.SuccessLatency.Count)
		js.FailureHistogram = histogramOf(pc.bounds, pc.failureHistogram, js.FailureLatency.Count)
	}
	return js
}

func summaryOf(count uint64, sum, min, max int64) LatencySummary {
	s := LatencySummary{
		Count: count,
		Sum:   time.Duration(sum),
		Min:   time.Duration(min),
		Max:   time.Duration(max),
	}
	if count > 0 {
		s.Avg = time.Duration(sum) / time.Duration(count)
	}
	return s
}

func histogramOf(bounds []time.Duration, counts []atomic.Uint64, total uint64) *Histogram {
	h := &Histogram{
		Bounds: append([]time.Duration(nil), bounds...),
		Counts: make([]uint64, len(counts)),
		Count:  total,
	}
	for i := range counts {
		h.Counts[i] = counts[i].Load()
	}
	return h
}

// forName returns the per-job-name counters for the given name, creating them
// on first use. The fast path is a lock-free map load; the insert path is
// serialized so the tracked count stays accurate. Once maxJobStats is reached,
// new names are redirected to the untracked aggregate bucket.
func (c *Collector) forName(name string) *perJobCounters {
	if v, ok := c.jobs.Load(name); ok {
		return v.(*perJobCounters)
	}

	c.insertMu.Lock()
	defer c.insertMu.Unlock()

	if v, ok := c.jobs.Load(name); ok {
		return v.(*perJobCounters)
	}

	if c.maxJobStats > 0 && c.tracked.Load() >= c.maxJobStats {
		if c.untracked == nil {
			c.untracked = newPerJobCounters(c.bounds)
		}
		return c.untracked
	}

	pc := newPerJobCounters(c.bounds)
	c.jobs.Store(name, pc)
	c.tracked.Add(1)
	return pc
}

// IncEnqueued increments the aggregate enqueued counter.
func (c *Collector) IncEnqueued() {
	c.enqueued.Add(1)
}

// IncEnqueuedFor increments the aggregate and per-job-name enqueued counters.
func (c *Collector) IncEnqueuedFor(name string) {
	c.enqueued.Add(1)
	c.forName(name).enqueued.Add(1)
}

// IncProcessed increments the aggregate processed counter.
func (c *Collector) IncProcessed() {
	c.processed.Add(1)
}

// IncProcessedFor increments the aggregate and per-job-name processed counters
// and records latency as a successful execution.
func (c *Collector) IncProcessedFor(name string, latency time.Duration) {
	c.processed.Add(1)
	pc := c.forName(name)
	pc.processed.Add(1)
	pc.observeSuccess(latency)
}

// IncFailed increments the aggregate failed counter.
func (c *Collector) IncFailed() {
	c.failed.Add(1)
}

// IncFailedFor increments the aggregate and per-job-name failed counters and
// records latency as a failed execution.
func (c *Collector) IncFailedFor(name string, latency time.Duration) {
	c.failed.Add(1)
	pc := c.forName(name)
	pc.failed.Add(1)
	pc.observeFailure(latency)
}

// IncRetried increments the aggregate retried counter.
func (c *Collector) IncRetried() {
	c.retried.Add(1)
}

// IncRetriedFor increments the aggregate and per-job-name retried counters.
func (c *Collector) IncRetriedFor(name string) {
	c.retried.Add(1)
	c.forName(name).retried.Add(1)
}

// IncDropped increments the aggregate dropped counter.
func (c *Collector) IncDropped() {
	c.dropped.Add(1)
}

// IncDroppedFor increments the aggregate and per-job-name dropped counters.
func (c *Collector) IncDroppedFor(name string) {
	c.dropped.Add(1)
	c.forName(name).dropped.Add(1)
}

// IncRetryPredicatesFailed increments the aggregate retryPredicatesFailed counter.
func (c *Collector) IncRetryPredicatesFailed() {
	c.retryPredicatesFailed.Add(1)
}

// IncRetryPredicatesFailedFor increments the aggregate and per-job-name
// retryPredicatesFailed counters.
func (c *Collector) IncRetryPredicatesFailedFor(name string) {
	c.retryPredicatesFailed.Add(1)
	c.forName(name).retryPredicatesFailed.Add(1)
}

// Snapshot returns a snapshot of the aggregate queue statistics.
func (c *Collector) Snapshot() Snapshot {
	return Snapshot{
		Enqueued:              c.enqueued.Load(),
		Processed:             c.processed.Load(),
		Failed:                c.failed.Load(),
		Retried:               c.retried.Load(),
		Dropped:               c.dropped.Load(),
		RetryPredicatesFailed: c.retryPredicatesFailed.Load(),
	}
}

// SnapshotByJobName returns a per-job-name breakdown of the queue statistics,
// sorted by name. The untracked aggregate bucket is included when it has any
// activity. The sum of the per-job-name counters plus the untracked bucket
// equals the aggregate Snapshot values once all in-flight operations settle.
func (c *Collector) SnapshotByJobName() []JobStat {
	var jobs []JobStat
	c.jobs.Range(func(key, value any) bool {
		jobs = append(jobs, value.(*perJobCounters).snapshot(key.(string)))
		return true
	})
	if c.untracked != nil {
		if u := c.untracked.snapshot(UntrackedBucket); !u.isZero() {
			jobs = append(jobs, u)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Name < jobs[j].Name })
	return jobs
}
