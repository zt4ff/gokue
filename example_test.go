package gokue_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/zt4ff/gokue"
)

type receiptEmail struct {
	to string
}

func (job receiptEmail) Process(context.Context) error {
	fmt.Println("Sending receipt to", job.to)
	return nil
}

func Example() {
	ctx := context.Background()
	queue, err := gokue.NewQueue(gokue.WithWorkerCount(1))
	if err != nil {
		panic(err)
	}

	if err := queue.Submit(ctx, receiptEmail{to: "customer@example.com"}); err != nil {
		panic(err)
	}
	// Close waits for accepted jobs to finish.
	if err := queue.Close(ctx); err != nil {
		panic(err)
	}

	// Output:
	// Sending receipt to customer@example.com
}

type exampleJobFunc func(context.Context) error

func (job exampleJobFunc) Process(ctx context.Context) error { return job(ctx) }

func ExampleQueue_Submit_retry() {
	ctx := context.Background()
	queue, err := gokue.NewQueue(
		gokue.WithMaxRetries(0),
		gokue.WithOnRetry(func(event gokue.JobEvent) {
			fmt.Printf("Attempt %d failed: %v\n", event.Attempt, event.Err)
		}),
	)
	if err != nil {
		panic(err)
	}

	unavailable := errors.New("billing service unavailable")
	attempts := 0
	syncInvoice := exampleJobFunc(func(context.Context) error {
		attempts++
		if attempts == 1 {
			return unavailable
		}
		fmt.Println("Invoice synced")
		return nil
	})
	// Allow one retry for this job, and only for a transient error.
	if err := queue.Submit(ctx, syncInvoice,
		gokue.MaxRetries(1),
		gokue.RetryDelay(0),
		gokue.RetryIf(func(err error) bool { return errors.Is(err, unavailable) }),
	); err != nil {
		panic(err)
	}
	if err := queue.Close(ctx); err != nil {
		panic(err)
	}

	stats := queue.Stats()
	fmt.Printf("Processed: %d, retried: %d\n", stats.Processed, stats.Retried)

	// Output:
	// Attempt 1 failed: billing service unavailable
	// Invoice synced
	// Processed: 1, retried: 1
}

func ExampleQueue_TrySubmit() {
	ctx := context.Background()
	queue, err := gokue.NewQueue(gokue.WithWorkerCount(1), gokue.WithQueueSize(1))
	if err != nil {
		panic(err)
	}

	started, release := make(chan struct{}), make(chan struct{})
	first := exampleJobFunc(func(ctx context.Context) error {
		close(started)
		<-release
		return (receiptEmail{to: "first@example.com"}).Process(ctx)
	})
	if err := queue.TrySubmit(ctx, first); err != nil {
		panic(err)
	}
	// Keep the only worker busy so the next receipt fills the waiting slot.
	<-started
	if err := queue.TrySubmit(ctx, receiptEmail{to: "second@example.com"}); err != nil {
		panic(err)
	}
	if err := queue.TrySubmit(ctx, receiptEmail{to: "third@example.com"}); errors.Is(err, gokue.ErrQueueFull) {
		fmt.Println("Queue full; try again later")
	} else if err != nil {
		panic(err)
	}

	close(release)
	if err := queue.Close(ctx); err != nil {
		panic(err)
	}

	// Output:
	// Queue full; try again later
	// Sending receipt to first@example.com
	// Sending receipt to second@example.com
}
