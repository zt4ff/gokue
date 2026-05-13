package logging

import (
	"testing"
)

// TestLoggerInterface verifies that the Logger interface can be implemented.
func TestLoggerInterface(t *testing.T) {
	var logger Logger
	var noop Logger = &NoOpLogger{}
	logger = noop

	if logger == nil {
		t.Error("logger should not be nil")
	}
}

// TestNoOpLogger verifies NoOpLogger implements the Logger interface.
func TestNoOpLogger(t *testing.T) {
	logger := &NoOpLogger{}

	// Should not panic
	logger.Log(LevelInfo, "test message", "key", "value")
	logger.Debug("debug", "x", "y")
	logger.Info("info", "a", "b")
	logger.Warn("warn", "c", "d")
	logger.Error("error", "e", "f")
}

// TestDefaultLoggerCreation verifies DefaultLogger can be created.
func TestDefaultLoggerCreation(t *testing.T) {
	logger := NewDefaultLogger(LevelInfo)
	if logger == nil {
		t.Error("logger should not be nil")
	}
}

// TestDefaultLoggerLevels verifies DefaultLogger respects log levels.
func TestDefaultLoggerLevels(t *testing.T) {
	// Debug logger should log all
	debugLogger := NewDefaultLogger(LevelDebug)
	debugLogger.Debug("debug test", "key", "value")
	debugLogger.Info("info test")
	debugLogger.Warn("warn test")
	debugLogger.Error("error test")

	// Info logger should skip debug
	infoLogger := NewDefaultLogger(LevelInfo)
	infoLogger.Debug("debug test") // should be skipped
	infoLogger.Info("info test")
	infoLogger.Warn("warn test")
	infoLogger.Error("error test")

	// Error logger should only log errors
	errorLogger := NewDefaultLogger(LevelError)
	errorLogger.Debug("debug test") // should be skipped
	errorLogger.Info("info test")   // should be skipped
	errorLogger.Warn("warn test")   // should be skipped
	errorLogger.Error("error test") // should log
}

// TestLogEventFlattening verifies LogEvent.Flatten() produces correct output.
func TestLogEventFlattening(t *testing.T) {
	event := &LogEvent{
		Message: "test",
		Level:   LevelInfo,
		JobName: "my-job",
		Attempt: 2,
	}

	msg, fields := event.Flatten()
	if msg != "test" {
		t.Errorf("expected message 'test', got %q", msg)
	}
	if len(fields) < 4 {
		t.Errorf("expected at least 4 fields, got %d", len(fields))
	}

	// Verify job_name is in fields
	found := false
	for i := 0; i < len(fields); i += 2 {
		if i+1 < len(fields) && fields[i] == "job_name" && fields[i+1] == "my-job" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected job_name field in flattened output")
	}
}

// TestLogHelperFunctions verifies log helper functions don't panic.
func TestLogHelperFunctions(t *testing.T) {
	logger := &NoOpLogger{}

	// These should not panic
	LogSubmitAccepted(logger, "job-name")
	LogSubmitRejected(logger, "job-name", "queue_full")
	LogJobProcessing(logger, "job-name", 1)
	LogJobRetry(logger, "job-name", 1, nil, 0)
	LogJobSuccess(logger, "job-name", 0)
	LogJobFailure(logger, "job-name", 3, nil, 0)
	LogCloseStart(logger, "drain")
	LogCloseComplete(logger, "drain", 0, nil)
	LogJobPanic(logger, "job-name", "something")
	LogWorkerPanic(logger, "panic value")
}

// TestLogHelperFunctionsWithNilLogger verifies log helper functions handle nil logger.
func TestLogHelperFunctionsWithNilLogger(t *testing.T) {
	var logger Logger = nil

	// These should not panic even with nil logger
	LogSubmitAccepted(logger, "job-name")
	LogSubmitRejected(logger, "job-name", "queue_full")
	LogJobProcessing(logger, "job-name", 1)
	LogJobRetry(logger, "job-name", 1, nil, 0)
	LogJobSuccess(logger, "job-name", 0)
	LogJobFailure(logger, "job-name", 3, nil, 0)
	LogCloseStart(logger, "drain")
	LogCloseComplete(logger, "drain", 0, nil)
	LogJobPanic(logger, "job-name", "something")
	LogWorkerPanic(logger, "panic value")
}

// TestDefaultLoggerWithFields verifies DefaultLogger handles odd number of fields.
func TestDefaultLoggerWithOddFields(t *testing.T) {
	logger := NewDefaultLogger(LevelInfo)

	// Should not panic with odd number of fields
	logger.Info("test message", "key1", "value1", "key2") // missing value for key2
}
