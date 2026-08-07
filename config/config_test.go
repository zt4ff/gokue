package config

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg.Backend != InMemory {
		t.Errorf("expected backend %q, got %q", InMemory, cfg.Backend)
	}
	if cfg.WorkerCount != runtime.GOMAXPROCS(0) {
		t.Errorf("expected worker count %d, got %d", runtime.GOMAXPROCS(0), cfg.WorkerCount)
	}
	if cfg.QueueSize != 1024 {
		t.Errorf("expected queue size 1024, got %d", cfg.QueueSize)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("expected max retries 3, got %d", cfg.MaxRetries)
	}
	if cfg.JobTimeout != 30*time.Second {
		t.Errorf("expected job timeout 30s, got %v", cfg.JobTimeout)
	}
	if cfg.RetryDelay != 250*time.Millisecond {
		t.Errorf("expected retry delay 250ms, got %v", cfg.RetryDelay)
	}
	if cfg.MaxRetryDelay != 30*time.Second {
		t.Errorf("expected max retry delay 30s, got %v", cfg.MaxRetryDelay)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("expected shutdown timeout 10s, got %v", cfg.ShutdownTimeout)
	}
	if cfg.BackoffStrategy != Exponential {
		t.Errorf("expected backoff strategy %q, got %q", Exponential, cfg.BackoffStrategy)
	}
}

func TestValidateValidConfigs(t *testing.T) {
	testcases := map[string]struct {
		config Config
	}{
		"default config": {
			config: Default(),
		},
		"in-memory with constant backoff": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       1,
				BackoffStrategy: Constant,
			},
		},
		"exponential-jitter backoff": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     2,
				QueueSize:       5,
				BackoffStrategy: ExponentialJitter,
			},
		},
		"zero values allowed for optional fields": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       1,
				MaxRetries:      0,
				JobTimeout:      0,
				RetryDelay:      0,
				MaxRetryDelay:   0,
				ShutdownTimeout: 0,
				BackoffStrategy: Exponential,
			},
		},
	}

	for name, testcase := range testcases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := testcase.config.Validate(); err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidateInvalidConfigs(t *testing.T) {
	testcases := map[string]struct {
		config Config
	}{
		"unsupported backend": {
			config: Config{
				Backend: "wrong backend",
			},
		},
		"empty backend": {
			config: Config{
				Backend: "",
			},
		},
		"unsupported backoff strategy": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       1,
				BackoffStrategy: "quadratic",
			},
		},
		"empty backoff strategy": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       1,
				BackoffStrategy: "",
			},
		},
		"zero worker count": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     0,
				QueueSize:       1,
				BackoffStrategy: Exponential,
			},
		},
		"negative worker count": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     -1,
				QueueSize:       1,
				BackoffStrategy: Exponential,
			},
		},
		"zero queue size": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       0,
				BackoffStrategy: Exponential,
			},
		},
		"negative queue size": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       -1,
				BackoffStrategy: Exponential,
			},
		},
		"negative max retries": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       1,
				MaxRetries:      -1,
				BackoffStrategy: Exponential,
			},
		},
		"negative job timeout": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       1,
				JobTimeout:      -1,
				BackoffStrategy: Exponential,
			},
		},
		"negative retry delay": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       1,
				RetryDelay:      -1,
				BackoffStrategy: Exponential,
			},
		},
		"negative max retry delay": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       1,
				MaxRetryDelay:   -1,
				BackoffStrategy: Exponential,
			},
		},
		"negative shutdown timeout": {
			config: Config{
				Backend:         InMemory,
				WorkerCount:     1,
				QueueSize:       1,
				ShutdownTimeout: -1,
				BackoffStrategy: Exponential,
			},
		},
	}

	for name, testcase := range testcases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := testcase.config.Validate()
			if err == nil {
				t.Fatal("expected error but got nil")
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("expected error to wrap %v, got %v", ErrInvalidConfig, err)
			}
		})
	}
}
