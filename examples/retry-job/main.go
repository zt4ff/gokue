package main

// Package main demonstrates how gokue handles job failures and retries.
import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zt4ff/gokue"
)

type Email struct {
	address string
	message string
}

// Process implements the gokue.Job interface.
func (e Email) Process(ctx context.Context) error {
	select {
	case <-time.After(500 * time.Millisecond):
		fmt.Printf("Failed to send email to %s\n", e.address)

		// Returning an error triggers the dispatcher's retry logic.
		return errors.New("simulated email service failure")

	case <-ctx.Done():
		fmt.Printf(
			"Email job for %s cancelled: %v\n",
			e.address,
			ctx.Err(),
		)
		return ctx.Err()
	}
}

func main() {
	q, err := gokue.NewQueue(
		gokue.WithWorkerCount(3),
		gokue.WithQueueSize(64),
		gokue.WithMaxRetries(4),
		gokue.WithRetryDelay(500*time.Millisecond),
		gokue.WithJobTimeout(2*time.Second),
	)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	job := Email{
		address: "johndoe@gmail.com",
		message: "This is testing the email",
	}

	if err := q.Submit(ctx, job); err != nil {
		panic(err)
	}

	// Drain the queue before reading the final statistics.
	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	if err := q.Close(shutdownCtx); err != nil {
		panic(err)
	}

	fmt.Println("\nExecution statistics:", q.Stats())
}
