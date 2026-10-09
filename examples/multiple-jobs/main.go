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
func (e Email) Process(context.Context) error {
	// Simulate email service
	time.Sleep(5 * time.Second)
	fmt.Printf("Sending %s to %s\n", e.message, e.address)
	return nil
}

func main() {
	emailsToSend := []Email{
		{address: "alice.johnson@example.com", message: "Welcome to our platform, Alice!"},
		{address: "michael.brown@example.com", message: "Your account has been created successfully."},
		{address: "sarah.williams@example.com", message: "Your password was updated successfully."},
		{address: "david.miller@example.com", message: "Your order has been confirmed."},
		{address: "emily.davis@example.com", message: "Your payment was received. Thank you!"},
		{address: "james.wilson@example.com", message: "Your weekly activity report is ready."},
		{address: "olivia.moore@example.com", message: "You have a new notification waiting."},
		{address: "daniel.taylor@example.com", message: "Your subscription expires in three days."},
		{address: "sophia.anderson@example.com", message: "Your support ticket has been received."},
		{address: "ethan.thomas@example.com", message: "Your requested report is attached."},
		{address: "ava.jackson@example.com", message: "Thanks for joining our newsletter!"},
		{address: "noah.white@example.com", message: "Your profile was updated successfully."},
		{address: "isabella.harris@example.com", message: "Your scheduled meeting starts tomorrow."},
		{address: "liam.martin@example.com", message: "Your delivery is on its way."},
		{address: "mia.thompson@example.com", message: "Your email address has been verified."},
		{address: "lucas.garcia@example.com", message: "Your trial period ends next week."},
	}

	q, err := gokue.NewQueue(
		gokue.WithWorkerCount(4),
		gokue.WithQueueSize(64),
		gokue.WithMaxRetries(2),
		gokue.WithJobTimeout(5*time.Second),
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

	for _, job := range emailsToSend {
		if err := q.Submit(ctx, job); err != nil {
			panic(err)
		}
	}

}
