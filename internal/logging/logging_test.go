package logging

import (
	"errors"
	"testing"
	"time"
)

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
		if i+1 < len(fields) && fields[i] == string(FieldJobName) && fields[i+1] == "my-job" {
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

// TestDefaultLoggerWithFields verifies DefaultLogger handles odd number of fields.
func TestDefaultLoggerWithOddFields(t *testing.T) {
	logger := NewDefaultLogger(LevelInfo)

	// Should not panic with odd number of fields
	logger.Info("test message", "key1", "value1", "key2") // missing value for key2
}

// TestLevelToString verifies levelToString covers every level and the fallback.
func TestLevelToString(t *testing.T) {
	cases := []struct {
		level Level
		want  string
	}{
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
		{Level(999), "UNKNOWN"},
	}
	for _, tc := range cases {
		if got := levelToString(tc.level); got != tc.want {
			t.Errorf("levelToString(%d): expected %q, got %q", tc.level, tc.want, got)
		}
	}
}

// TestLogEventFlattenAllBranches verifies Flatten includes error, duration, and
// extra keys when present.
func TestLogEventFlattenAllBranches(t *testing.T) {
	event := &LogEvent{
		Message:   "full",
		Level:     LevelInfo,
		JobName:   "my-job",
		Attempt:   3,
		Error:     errors.New("boom"),
		Duration:  250 * time.Millisecond,
		ExtraKeys: []interface{}{"extra", "value"},
	}

	msg, fields := event.Flatten()
	if msg != "full" {
		t.Errorf("expected message 'full', got %q", msg)
	}

	flat := map[string]interface{}{}
	for i := 0; i < len(fields); i += 2 {
		flat[fields[i].(string)] = fields[i+1]
	}
	if flat[string(FieldError)] != "boom" {
		t.Errorf("expected error field, got %v", flat[string(FieldError)])
	}
	if flat[string(FieldDurationMs)] != int64(250) {
		t.Errorf("expected duration 250ms, got %v", flat[string(FieldDurationMs)])
	}
	if flat["extra"] != "value" {
		t.Errorf("expected extra key, got %v", flat["extra"])
	}
}

// TestLogEventFlattenEmpty verifies Flatten handles an empty event without panicking.
func TestLogEventFlattenEmpty(t *testing.T) {
	event := &LogEvent{Message: "empty"}
	msg, fields := event.Flatten()
	if msg != "empty" {
		t.Errorf("expected message 'empty', got %q", msg)
	}
	if len(fields) != 0 {
		t.Errorf("expected no fields, got %v", fields)
	}
}

// TestLogHelpersWithErrors verifies helpers that render error strings work when
// err is non-nil.
func TestLogHelpersWithErrors(t *testing.T) {
	logger := &NoOpLogger{}
	jobErr := errors.New("boom")

	LogJobRetry(logger, "job-name", 1, jobErr, 5*time.Millisecond)
	LogJobFailure(logger, "job-name", 1, jobErr, 5*time.Millisecond)
	LogCloseComplete(logger, "drain", time.Millisecond, jobErr)
	LogJobProcessing(logger, "job-name", 1)
	LogSubmitEnqueued(logger, "job-name")
	LogSubmitRejected(logger, "job-name", ReasonContextCancelled)
}
