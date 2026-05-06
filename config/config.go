package config

import (
	"errors"
	"fmt"
	"runtime"
	"time"
)

const (
	InMemory = "in-memory"
	MongoDB  = "mongo-db"
)

type Config struct {
	Backend         string
	WorkerCount     int
	QueueSize       int
	MaxRetries      int
	JobTimeout      time.Duration
	RetryDelay      time.Duration
	ShutdownTimeout time.Duration
}

var ErrInvalidConfig = errors.New("invalid queue config")

func Default() Config {
	return Config{
		Backend:         InMemory,
		WorkerCount:     runtime.GOMAXPROCS(0),
		QueueSize:       1024,
		MaxRetries:      3,
		JobTimeout:      30 * time.Second,
		RetryDelay:      250 * time.Millisecond,
		ShutdownTimeout: 10 * time.Second,
	}
}

func (c *Config) Normalize() {
	if c.Backend == "" {
		c.Backend = InMemory
	}
	if c.WorkerCount <= 0 {
		c.WorkerCount = runtime.GOMAXPROCS(0)
	}
	if c.QueueSize <= 0 {
		c.QueueSize = 1024
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.JobTimeout < 0 {
		c.JobTimeout = 0
	}
	if c.RetryDelay < 0 {
		c.RetryDelay = 0
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = 10 * time.Second
	}
}

func (c *Config) Validate() error {
	c.Normalize()

	switch c.Backend {
	case InMemory, MongoDB:
	default:
		return fmt.Errorf("%w: unsupported backend %q", ErrInvalidConfig, c.Backend)
	}

	if c.WorkerCount <= 0 {
		return fmt.Errorf("%w: worker count must be greater than zero", ErrInvalidConfig)
	}
	if c.QueueSize <= 0 {
		return fmt.Errorf("%w: queue size must be greater than zero", ErrInvalidConfig)
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("%w: max retries cannot be negative", ErrInvalidConfig)
	}

	return nil
}
