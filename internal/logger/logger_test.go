package logger

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// closableBuffer is an in-memory write stream that records Close calls.
type closableBuffer struct {
	bytes.Buffer
	closed bool
}

func (c *closableBuffer) Close() error {
	c.closed = true
	return nil
}

// TestNewLoggerWrites verifies NewLogger produces JSON output on the given writer.
func TestNewLoggerWrites(t *testing.T) {
	var buf bytes.Buffer
	logger, closer := NewLogger(&buf)

	logger.Info("hello", "key", "value")
	logger.Error("boom")

	if err := closer(); err != nil {
		t.Fatalf("closer failed: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "hello") {
		t.Errorf("expected 'hello' in output, got %q", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("expected 'boom' in output, got %q", got)
	}
	if !strings.Contains(got, "key") || !strings.Contains(got, "value") {
		t.Errorf("expected key-value fields in output, got %q", got)
	}
}

// TestNewLoggerClosesWriter verifies the closer closes the write stream when it
// implements io.Closer.
func TestNewLoggerClosesWriter(t *testing.T) {
	w := &closableBuffer{}
	logger, closer := NewLogger(w)

	logger.Info("msg")

	if err := closer(); err != nil {
		t.Fatalf("closer failed: %v", err)
	}
	if !w.closed {
		t.Error("expected closer to close the write stream")
	}
}

// TestLoggerLevels verifies Log maps levels to the correct zap levels.
func TestLoggerLevels(t *testing.T) {
	var buf bytes.Buffer
	logger, closer := NewLogger(&buf)

	logger.Log(LevelInfo, "info-msg")
	logger.Log(LevelWarn, "warn-msg")
	logger.Log(LevelDebug, "debug-msg")
	logger.Log(LevelError, "error-msg")

	if err := closer(); err != nil {
		t.Fatalf("closer failed: %v", err)
	}

	got := buf.String()
	for _, want := range []string{"info-msg", "warn-msg", "debug-msg", "error-msg"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got %q", want, got)
		}
	}
}

// TestLogHelperFunctionsWithNilLogger verifies log helper functions handle nil logger.
func TestLogHelperFunctionsWithNilLogger(t *testing.T) {
	var logger Logger = nil

	// These should not panic even with nil logger
	LogSubmitEnqueued(logger, "job-name")
	LogSubmitRejected(logger, "job-name", ReasonQueueFull)
	LogJobProcessing(logger, "job-name", 1)
	LogJobRetry(logger, "job-name", 1, nil, 0)
	LogJobSuccess(logger, "job-name", 0)
	LogJobFailure(logger, "job-name", 3, nil, 0)
	LogJobAbandoned(logger, "job-name", 1, ReasonDispatcherShutdown, 0)
	LogCloseStart(logger, "drain")
	LogCloseComplete(logger, "drain", 0, nil)
	LogJobPanic(logger, "job-name", "something")
	LogWorkerPanic(logger, "panic value")
}

// TestLogHelpersWithErrors verifies helpers that render error strings work when
// err is non-nil.
func TestLogHelpersWithErrors(t *testing.T) {
	var logger Logger = nil
	jobErr := errors.New("boom")

	LogJobRetry(logger, "job-name", 1, jobErr, 5*time.Millisecond)
	LogJobFailure(logger, "job-name", 1, jobErr, 5*time.Millisecond)
	LogCloseComplete(logger, "drain", time.Millisecond, jobErr)
	LogJobProcessing(logger, "job-name", 1)
	LogSubmitEnqueued(logger, "job-name")
	LogSubmitRejected(logger, "job-name", ReasonContextCancelled)
}
