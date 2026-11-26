// Package main shows a persistent queue: jobs are saved to a backend.JobStore
// when submitted, so jobs a process did not get to are run by the next queue
// opened on the same store.
//
// It uses backend.MemoryStore so it runs without a database; both queues live
// in one process to stand in for a restart. For jobs that survive a real
// restart, use the MongoDB store (github.com/zt4ff/gokue/backend/mongodb/v2)
// or another JobStore implementation.
//
//	go run ./examples/persistence
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/zt4ff/gokue/v2"
	"github.com/zt4ff/gokue/v2/backend"
)

// Invoice is saved as JSON, so its fields must be exported.
type Invoice struct {
	ID     int
	Amount int
}

// Process implements gokue.Job. Persistent queues deliver at least once, so a
// real job should be safe to run twice (for example, keyed by Invoice.ID).
func (i Invoice) Process(context.Context) error {
	time.Sleep(200 * time.Millisecond) // stands in for a payment API call
	log.Printf("charged invoice %d (%d cents)", i.ID, i.Amount)
	return nil
}

func main() {
	ctx := context.Background()
	store := backend.NewMemoryStore()
	codec := backend.NewCodec()
	// Every job type a persistent queue runs must be registered.
	if err := codec.Register("", Invoice{}); err != nil {
		log.Fatal(err)
	}

	// The first "process" submits five invoices and shuts down at once.
	// Close finishes the job its single worker already claimed and leaves
	// the rest pending in the store.
	first, err := gokue.NewQueue(gokue.WithStore(store, codec), gokue.WithWorkerCount(1))
	if err != nil {
		log.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		if err := first.Submit(ctx, Invoice{ID: i, Amount: i * 1000}); err != nil {
			log.Fatal(err)
		}
	}
	if err := first.Close(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("pending after first process: %d\n", count(ctx, store, backend.StatusPending))

	// The second "process" opens a queue on the same store and runs them.
	second, err := gokue.NewQueue(
		gokue.WithStore(store, codec),
		gokue.WithWorkerCount(2),
		gokue.WithPollInterval(50*time.Millisecond),
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := second.Submit(ctx, Invoice{ID: 6, Amount: 6000}); err != nil {
		log.Fatal(err)
	}
	for count(ctx, store, backend.StatusSucceeded) < 6 {
		time.Sleep(20 * time.Millisecond)
	}
	if err := second.Close(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("succeeded after second process: %d\n", count(ctx, store, backend.StatusSucceeded))
}

func count(ctx context.Context, store backend.JobStore, status backend.JobStatus) int {
	recs, err := store.List(ctx, backend.ListFilter{Status: &status})
	if err != nil {
		log.Fatal(err)
	}
	return len(recs)
}
