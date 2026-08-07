package main

import (
	"context"
	"testing"
)

func TestMain(t *testing.T) {
	main()
}

func TestEmailProcess(t *testing.T) {
	email := Email{email: "test@example.com", message: "hello"}
	if err := email.Process(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
