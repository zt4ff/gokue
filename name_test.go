package gokue

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestValidateJobName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"my-job", false},
		{"My_Job.1", false},
		{"email runner", false},
		{"a", false},
		{"", true},
		{"job with @ invalid", true},
		{"job\nwith\nnewlines", true},
		{"job\twith\ttabs", true},
		{string(make([]byte, 256)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJobName(tt.name)
			if tt.wantErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRegisterJobValidation(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"valid-job", false},
		{"", true},
		{"  ", true},
		{string(make([]byte, 256)), true},
		{"invalid@char", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := NewQueue()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			err = q.RegisterJob(tt.name)
			if tt.wantErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestSubmitRejectsInvalidName(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	err = queue.Submit(context.Background(), "invalid@name", &successJob{processed: &atomic.Bool{}})
	if err == nil {
		t.Error("expected error for invalid job name")
	}
}

func TestRunWithNilTask(t *testing.T) {
	queue, err := NewQueue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer queue.Close(context.Background())

	queue.Run(nil)
}
