// Package main demonstrates a simple example of using the gokue job queue to send emails.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/zt4ff/gokue"
)

// Email represents an email job to be processed.
type Email struct {
	// email is the recipient email address.
	address string
	// message is the email body to send.
	message string
}

// Process implements the gokue.Job interface and sends an email.
func (e Email) Process(ctx context.Context) error {
	// Simulate email service
	select {
	case <- time.After(10 * time.Second):
		fmt.Printf("Sending %s to %s\n", e.message, e.address)
	case <- ctx.Done():
		fmt.Printf("Failed to send %s to %s: %v\n", e.message, e.address, ctx.Err())
		return ctx.Err()
	}
	return nil
}

// main creates a job queue and submits an email task for processing.
func main() {
	q, err := gokue.NewQueue(
		gokue.WithWorkerCount(1),
		gokue.WithQueueSize(64),
		gokue.WithMaxRetries(2),
		gokue.WithJobTimeout(2*time.Second),
	)
	if err != nil {
		// Handle error
		panic(err)
	}

	ctx := context.Background()

	defer func() {
		if err := q.Close(ctx); err != nil {
			panic(err)
		}
	}()

	job := Email{address: "johndoe@gmail.com", message: "This is testing the email"}

	if err := q.Submit(ctx, job); err != nil {
		panic(err)
	}
}
