package stats

import (
	"sync"
	"testing"
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
