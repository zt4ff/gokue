// Package main shows the recommended way to stop a gokue queue: wire SIGINT and
// SIGTERM to a context, stop producing work when it is cancelled, then drain
// the queue with a deadline and fall back to an immediate stop.
//
// Run it and press Ctrl+C (or send SIGTERM). Pass -run to stop on its own
// after a fixed time instead.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zt4ff/gokue/v2"
)

// Report is a job that takes a moment to finish.
type Report struct {
	ID int
}

// Process implements gokue.Job. It honours ctx so a job timeout can stop it.
func (r Report) Process(ctx context.Context) error {
	select {
	case <-time.After(500 * time.Millisecond):
		log.Printf("report %d done", r.ID)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func main() {
	runFor := flag.Duration("run", 0, "stop after this long without a signal (0 waits for a signal)")
	drainTimeout := flag.Duration("drain", 10*time.Second, "how long to wait for queued jobs on shutdown")
	flag.Parse()

	// ctx is cancelled on the first SIGINT or SIGTERM. A second signal kills
	// the process with the default behaviour because stop restores it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *runFor > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *runFor)
		defer cancel()
	}

	q, err := gokue.NewQueue(
		gokue.WithWorkerCount(2),
		gokue.WithQueueSize(16),
		gokue.WithJobTimeout(5*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}

	produce(ctx, q)
	stop()

	log.Printf("shutting down: draining up to %s", *drainTimeout)
	drainCtx, cancel := context.WithTimeout(context.Background(), *drainTimeout)
	defer cancel()

	if err := q.Close(drainCtx); err != nil {
		// The drain deadline passed: stop waiting for the remaining jobs.
		log.Printf("drain did not finish (%v); stopping immediately", err)
		_ = q.CloseWithMode(context.Background(), gokue.ImmediateMode)
		os.Exit(1)
	}

	s := q.Stats()
	log.Printf("stopped cleanly: %d processed, %d failed", s.Processed, s.Failed)
}

// produce submits a job every 200ms until ctx is cancelled. Submit takes ctx so
// a producer blocked on a full queue is released by the signal too.
func produce(ctx context.Context, q *gokue.Queue) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for id := 1; ; id++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := q.Submit(ctx, Report{ID: id}); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			log.Printf("submit report %d: %v", id, err)
		}
	}
}
