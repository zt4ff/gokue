package dispatcher_test

import (
	"context"
	"testing"
	"time"

	"github.com/zt4ff/gokue/config"
	"github.com/zt4ff/gokue/dispatcher"
	"github.com/zt4ff/gokue/stats"
)

func mustWait(t *testing.T, cond func() bool) {
	t.Helper()
	if !waitFor(t, 2*time.Second, cond) {
		t.Fatal("condition not met before deadline")
	}
}

func TestStatsLiveGauges(t *testing.T) {
	cfg := config.Default()
	cfg.WorkerCount = 2
	cfg.QueueSize = 4
	d := dispatcher.NewDispatcher(cfg, stats.NewCollector())

	mustWait(t, func() bool { return d.Stats().IdleWorkers == 2 })

	release := make(chan struct{})
	block := jobFunc(func(context.Context) error { <-release; return nil })
	for i := 0; i < 4; i++ {
		if err := d.Submit(context.Background(), dispatcher.Task{Name: "block", Job: block}); err != nil {
			t.Fatal(err)
		}
	}

	mustWait(t, func() bool { return d.Stats().Running == 2 })
	s := d.Stats()
	if s.QueueDepth != 2 || s.IdleWorkers != 0 {
		t.Fatalf("busy gauges = depth %d, running %d, idle %d; want 2, 2, 0", s.QueueDepth, s.Running, s.IdleWorkers)
	}

	close(release)
	mustWait(t, func() bool { s := d.Stats(); return s.Processed == 4 && s.Running == 0 })
	if s := d.Stats(); s.QueueDepth != 0 || s.IdleWorkers != 2 {
		t.Fatalf("idle gauges = %+v", s)
	}

	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := d.Stats(); s.IdleWorkers != 0 || s.Running != 0 || s.QueueDepth != 0 {
		t.Fatalf("gauges after Close = %+v", s)
	}
}
