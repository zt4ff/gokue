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
	email string
	// message is the email body to send.
	message string
}

// Process implements the gokue.Job interface and sends an email.
func (e Email) Process(context.Context) error {
	time.Sleep(2 * time.Second)
	fmt.Printf("Sending %s to %s\n", e.message, e.email)
	return nil
}

// main creates a job queue, registers the email job, and submits an email task for processing.
func main() {
	q, err := gokue.NewQueue(
		gokue.WithWorkerCount(1),
		gokue.WithQueueSize(64),
		gokue.WithMaxRetries(2),
		gokue.WithJobTimeout(5*time.Second),
	)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := q.Close(context.Background()); err != nil {
			panic(err)
		}
	}()

	user := Email{email: "johndoe@gmail.com", message: "This is testing the email"}

	q.RegisterJob("send email")

	if err := q.Submit(context.Background(), "send email", user); err != nil {
		panic(err)
	}
}
