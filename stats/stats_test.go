package stats

import (
	"sync"
	"testing"
	"time"
)

func TestNewCollector(t *testing.T) {
	c := NewCollector()
	if c == nil {
		t.Fatal("collector should not be nil")
	}

	snap := c.Snapshot()
	if snap.Enqueued != 0 || snap.Processed != 0 || snap.Failed != 0 || snap.Retried != 0 || snap.Dropped != 0 {
		t.Errorf("expected zero snapshot, got %+v", snap)
	}
}

func TestCollectorIncrements(t *testing.T) {
	testcases := map[string]struct {
		increment func(*Collector)
		field     func(Snapshot) uint64
	}{
		"enqueued": {
			increment: (*Collector).IncEnqueued,
			field:     func(s Snapshot) uint64 { return s.Enqueued },
		},
		"processed": {
			increment: (*Collector).IncProcessed,
			field:     func(s Snapshot) uint64 { return s.Processed },
		},
		"failed": {
			increment: (*Collector).IncFailed,
			field:     func(s Snapshot) uint64 { return s.Failed },
		},
		"retried": {
			increment: (*Collector).IncRetried,
			field:     func(s Snapshot) uint64 { return s.Retried },
		},
		"dropped": {
			increment: (*Collector).IncDropped,
			field:     func(s Snapshot) uint64 { return s.Dropped },
		},
		"retry predicates failed": {
			increment: (*Collector).IncRetryPredicatesFailed,
			field:     func(s Snapshot) uint64 { return s.RetryPredicatesFailed },
		},
	}

	for name, testcase := range testcases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := NewCollector()
			for i := 0; i < 10; i++ {
				testcase.increment(c)
			}
			snap := c.Snapshot()
			if got := testcase.field(snap); got != 10 {
				t.Errorf("expected counter value 10, got %d", got)
			}
		})
	}
}

func TestCollectorSnapshot(t *testing.T) {
	c := NewCollector()

	c.IncEnqueued()
	c.IncEnqueued()
	c.IncEnqueued()
	c.IncProcessed()
	c.IncFailed()
	c.IncRetried()
	c.IncRetried()
	c.IncDropped()
	c.IncRetryPredicatesFailed()

	snap := c.Snapshot()
	if snap.Enqueued != 3 {
		t.Errorf("expected Enqueued 3, got %d", snap.Enqueued)
	}
	if snap.Processed != 1 {
		t.Errorf("expected Processed 1, got %d", snap.Processed)
	}
	if snap.Failed != 1 {
		t.Errorf("expected Failed 1, got %d", snap.Failed)
	}
	if snap.Retried != 2 {
		t.Errorf("expected Retried 2, got %d", snap.Retried)
	}
	if snap.Dropped != 1 {
		t.Errorf("expected Dropped 1, got %d", snap.Dropped)
	}
	if snap.RetryPredicatesFailed != 1 {
		t.Errorf("expected RetryPredicatesFailed 1, got %d", snap.RetryPredicatesFailed)
	}
}

func TestCollectorConcurrent(t *testing.T) {
	c := NewCollector()

	const goroutines = 8
	const iterations = 1000

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				c.IncEnqueued()
				c.IncProcessed()
				c.IncFailed()
				c.IncRetried()
				c.IncDropped()
			}
		}()
	}
	wg.Wait()

	snap := c.Snapshot()
	expected := goroutines * iterations
	if snap.Enqueued != uint64(expected) {
		t.Errorf("expected Enqueued %d, got %d", expected, snap.Enqueued)
	}
	if snap.Processed != uint64(expected) {
		t.Errorf("expected Processed %d, got %d", expected, snap.Processed)
	}
	if snap.Failed != uint64(expected) {
		t.Errorf("expected Failed %d, got %d", expected, snap.Failed)
	}
	if snap.Retried != uint64(expected) {
		t.Errorf("expected Retried %d, got %d", expected, snap.Retried)
	}
	if snap.Dropped != uint64(expected) {
		t.Errorf("expected Dropped %d, got %d", expected, snap.Dropped)
	}
}

func TestSnapshotByJobName(t *testing.T) {
	c := NewCollector()

	c.IncEnqueuedFor("email")
	c.IncEnqueuedFor("email")
	c.IncProcessedFor("email", 10*time.Millisecond)
	c.IncRetriedFor("email")
	c.IncEnqueuedFor("sms")
	c.IncFailedFor("sms", 20*time.Millisecond)
	c.IncDroppedFor("sms")
	c.IncRetryPredicatesFailedFor("sms")

	jobs := c.SnapshotByJobName()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 job stats, got %d", len(jobs))
	}
	if jobs[0].Name != "email" || jobs[1].Name != "sms" {
		t.Fatalf("expected sorted names email,sms, got %q,%q", jobs[0].Name, jobs[1].Name)
	}

	email := jobs[0]
	if email.Enqueued != 2 || email.Processed != 1 || email.Retried != 1 {
		t.Errorf("unexpected email stats: %+v", email)
	}
	if email.SuccessLatency.Count != 1 || email.SuccessLatency.Sum != 10*time.Millisecond {
		t.Errorf("unexpected email success latency: %+v", email.SuccessLatency)
	}

	sms := jobs[1]
	if sms.Enqueued != 1 || sms.Failed != 1 || sms.Dropped != 1 || sms.RetryPredicatesFailed != 1 {
		t.Errorf("unexpected sms stats: %+v", sms)
	}
	if sms.FailureLatency.Count != 1 || sms.FailureLatency.Sum != 20*time.Millisecond {
		t.Errorf("unexpected sms failure latency: %+v", sms.FailureLatency)
	}

	agg := c.Snapshot()
	if agg.Enqueued != 3 || agg.Processed != 1 || agg.Failed != 1 || agg.Retried != 1 || agg.Dropped != 1 || agg.RetryPredicatesFailed != 1 {
		t.Errorf("unexpected aggregate snapshot: %+v", agg)
	}
}

func TestSnapshotByJobNameMinMaxAvg(t *testing.T) {
	c := NewCollector()

	c.IncProcessedFor("a", 5*time.Millisecond)
	c.IncProcessedFor("a", 15*time.Millisecond)
	c.IncFailedFor("a", 30*time.Millisecond)

	a := c.SnapshotByJobName()[0]
	if a.SuccessLatency.Min != 5*time.Millisecond || a.SuccessLatency.Max != 15*time.Millisecond || a.SuccessLatency.Avg != 10*time.Millisecond {
		t.Errorf("unexpected success latency: %+v", a.SuccessLatency)
	}
	if a.FailureLatency.Count != 1 || a.FailureLatency.Min != 30*time.Millisecond || a.FailureLatency.Max != 30*time.Millisecond {
		t.Errorf("unexpected failure latency: %+v", a.FailureLatency)
	}
}

func TestSnapshotByJobNameCardinalityCap(t *testing.T) {
	c := NewCollector(WithMaxJobStats(2))

	for _, name := range []string{"a", "b", "c", "d", "e"} {
		c.IncEnqueuedFor(name)
	}
	c.IncProcessedFor("c", time.Millisecond)

	jobs := c.SnapshotByJobName()
	if len(jobs) != 3 {
		t.Fatalf("expected 3 entries (a, b, untracked), got %d", len(jobs))
	}
	names := map[string]JobStat{}
	for _, j := range jobs {
		names[j.Name] = j
	}
	if a, ok := names["a"]; !ok || a.Enqueued != 1 {
		t.Errorf("expected tracked name a with 1 enqueued, got %+v", a)
	}
	if b, ok := names["b"]; !ok || b.Enqueued != 1 {
		t.Errorf("expected tracked name b with 1 enqueued, got %+v", b)
	}
	u, ok := names[UntrackedBucket]
	if !ok {
		t.Fatal("expected untracked bucket")
	}
	if u.Enqueued != 3 {
		t.Errorf("expected untracked Enqueued 3 (c,d,e), got %d", u.Enqueued)
	}
	if u.Processed != 1 {
		t.Errorf("expected untracked Processed 1, got %d", u.Processed)
	}
}

func TestSnapshotByJobNameCardinalityUnlimited(t *testing.T) {
	c := NewCollector()

	for _, name := range []string{"a", "b", "c"} {
		c.IncEnqueuedFor(name)
	}

	jobs := c.SnapshotByJobName()
	if len(jobs) != 3 {
		t.Errorf("expected 3 tracked names with no cap, got %d", len(jobs))
	}
	for _, j := range jobs {
		if j.Name == UntrackedBucket {
			t.Error("unexpected untracked bucket when no cap is set")
		}
	}
}

func TestSnapshotByJobNameConcurrent(t *testing.T) {
	c := NewCollector(WithMaxJobStats(32))

	names := []string{"a", "b", "c", "d", "e"}
	const goroutines = 8
	const iterations = 500

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				name := names[j%len(names)]
				c.IncEnqueuedFor(name)
				c.IncProcessedFor(name, time.Duration(j)*time.Nanosecond)
				c.IncRetriedFor(name)
			}
		}()
	}

	// Snapshot concurrently while writes are in flight.
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				c.SnapshotByJobName()
			}
		}
	}()

	wg.Wait()
	close(done)

	// After quiescence the per-name counters must reconcile with the aggregates.
	jobs := c.SnapshotByJobName()
	var enqueued, processed, retried uint64
	for _, j := range jobs {
		enqueued += j.Enqueued
		processed += j.Processed
		retried += j.Retried
	}
	agg := c.Snapshot()
	if enqueued != agg.Enqueued || processed != agg.Processed || retried != agg.Retried {
		t.Errorf("per-name totals %d/%d/%d do not reconcile with aggregate %d/%d/%d",
			enqueued, processed, retried, agg.Enqueued, agg.Processed, agg.Retried)
	}
}
