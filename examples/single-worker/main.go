package main

import (
	"context"
	"fmt"
	"time"

	"github.com/zt4ff/gokue"
)

type Email struct {
	email   string
	message string
}

func (e Email) Process(context.Context) error {
	time.Sleep(2 * time.Second)
	fmt.Printf("Sending %s to %s\n", e.message, e.email)
	return nil
}

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
