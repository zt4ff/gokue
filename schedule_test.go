package gokue

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDelayOptionHoldsJobAndCountsTowardsHealth(t *testing.T) {
	q, err := NewQueue(WithWorkerCount(1), WithQueueSize(1))
	if err != nil {
		t.Fatal(err)
	}
	defer q.CloseWithMode(context.Background(), ImmediateMode)

	done := make(chan struct{})
	j := blockingJob{release: done}
	close(done)
	if err := q.TrySubmit(context.Background(), j, Delay(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if s := q.Stats(); s.Scheduled != 1 || s.Processed != 0 {
		t.Fatalf("stats = %+v, want 1 scheduled", s)
	}
	if h := q.Health(); h.Status != HealthSaturated {
		t.Fatalf("Health = %+v, want saturated", h)
	}
	if err := q.TrySubmit(context.Background(), j, RunAt(time.Now())); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("TrySubmit = %v, want ErrQueueFull", err)
	}
	if err := q.Submit(context.Background(), j, Delay(-time.Second)); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("Submit = %v, want ErrInvalidTask", err)
	}
}
