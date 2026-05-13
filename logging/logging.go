// Package logging provides structured logging for the gokue job queue.
package logging

import (
	"fmt"
	"log"
	"time"
)

// Level represents the logging level.
type Level int

const (
	// LevelDebug represents debug level logs.
	LevelDebug Level = iota
	// LevelInfo represents info level logs.
	LevelInfo
	// LevelWarn represents warning level logs.
	LevelWarn
	// LevelError represents error level logs.
	LevelError
)

// Logger is the interface for structured logging in the dispatcher.
// Implementations should support structured fields for filtering and analysis.
type Logger interface {
	// Log records a log message with level and fields.
	// Fields should be in key-value pairs: key1, value1, key2, value2, ...
	Log(level Level, message string, fields ...interface{})

	// Debug logs at debug level.
	Debug(message string, fields ...interface{})
	// Info logs at info level.
	Info(message string, fields ...interface{})
	// Warn logs at warning level.
	Warn(message string, fields ...interface{})
	// Error logs at error level.
	Error(message string, fields ...interface{})
}

// NoOpLogger is a logger that does nothing. Use this to disable logging.
type NoOpLogger struct{}

// Log implements Logger.
func (n *NoOpLogger) Log(level Level, message string, fields ...interface{}) {}

// Debug implements Logger.
func (n *NoOpLogger) Debug(message string, fields ...interface{}) {}

// Info implements Logger.
func (n *NoOpLogger) Info(message string, fields ...interface{}) {}

// Warn implements Logger.
func (n *NoOpLogger) Warn(message string, fields ...interface{}) {}

// Error implements Logger.
func (n *NoOpLogger) Error(message string, fields ...interface{}) {}

// DefaultLogger is a simple structured logger that writes to the standard log package.
// It's suitable for development and basic production use.
type DefaultLogger struct {
	level Level
}

// NewDefaultLogger creates a new DefaultLogger with the specified level.
func NewDefaultLogger(level Level) *DefaultLogger {
	return &DefaultLogger{level: level}
}

// Log implements Logger.
func (dl *DefaultLogger) Log(level Level, message string, fields ...interface{}) {
	if level < dl.level {
		return
	}

	levelStr := levelToString(level)
	msg := fmt.Sprintf("[%s] %s", levelStr, message)

	// Append fields as key=value pairs
	if len(fields) > 0 {
		msg += " |"
		for i := 0; i < len(fields); i += 2 {
			if i+1 < len(fields) {
				msg += fmt.Sprintf(" %v=%v", fields[i], fields[i+1])
			} else {
				// Odd number of fields
				msg += fmt.Sprintf(" %v=<missing>", fields[i])
			}
		}
	}

	log.Println(msg)
}

// Debug implements Logger.
func (dl *DefaultLogger) Debug(message string, fields ...interface{}) {
	dl.Log(LevelDebug, message, fields...)
}

// Info implements Logger.
func (dl *DefaultLogger) Info(message string, fields ...interface{}) {
	dl.Log(LevelInfo, message, fields...)
}

// Warn implements Logger.
func (dl *DefaultLogger) Warn(message string, fields ...interface{}) {
	dl.Log(LevelWarn, message, fields...)
}

// Error implements Logger.
func (dl *DefaultLogger) Error(message string, fields ...interface{}) {
	dl.Log(LevelError, message, fields...)
}

func levelToString(level Level) string {
	switch level {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// LogEvent represents a structured log event with common fields for job processing.
type LogEvent struct {
	Message   string
	Level     Level
	JobName   string
	Attempt   int
	Error     error
	Duration  time.Duration
	ExtraKeys []interface{}
}

// Flatten converts a LogEvent to a flat key-value slice for the Logger.
func (le *LogEvent) Flatten() (message string, fields []interface{}) {
	message = le.Message
	fields = make([]interface{}, 0)

	if le.JobName != "" {
		fields = append(fields, "job_name", le.JobName)
	}
	if le.Attempt > 0 {
		fields = append(fields, "attempt", le.Attempt)
	}
	if le.Error != nil {
		fields = append(fields, "error", le.Error.Error())
	}
	if le.Duration > 0 {
		fields = append(fields, "duration_ms", le.Duration.Milliseconds())
	}
	fields = append(fields, le.ExtraKeys...)

	return
}

// LogSubmitAccepted logs a successful job submission.
func LogSubmitAccepted(logger Logger, jobName string) {
	if logger == nil {
		return
	}
	logger.Info("job_submitted", "job_name", jobName, "status", "accepted")
}

// LogSubmitRejected logs a rejected job submission.
func LogSubmitRejected(logger Logger, jobName string, reason string) {
	if logger == nil {
		return
	}
	logger.Warn("job_submission_rejected", "job_name", jobName, "reason", reason)
}

// LogJobProcessing logs the start of job execution.
func LogJobProcessing(logger Logger, jobName string, attempt int) {
	if logger == nil {
		return
	}
	logger.Debug("job_processing_started", "job_name", jobName, "attempt", attempt)
}

// LogJobRetry logs a job retry attempt.
func LogJobRetry(logger Logger, jobName string, attempt int, err error, delay time.Duration) {
	if logger == nil {
		return
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	logger.Info("job_retry",
		"job_name", jobName,
		"attempt", attempt,
		"error", errStr,
		"retry_delay_ms", delay.Milliseconds())
}

// LogJobSuccess logs successful job completion.
func LogJobSuccess(logger Logger, jobName string, duration time.Duration) {
	if logger == nil {
		return
	}
	logger.Info("job_completed",
		"job_name", jobName,
		"status", "success",
		"duration_ms", duration.Milliseconds())
}

// LogJobFailure logs final job failure after all retries.
func LogJobFailure(logger Logger, jobName string, attempt int, err error, duration time.Duration) {
	if logger == nil {
		return
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	logger.Error("job_failed",
		"job_name", jobName,
		"attempt", attempt,
		"error", errStr,
		"duration_ms", duration.Milliseconds(),
		"status", "final_failure")
}

// LogCloseStart logs the start of dispatcher shutdown.
func LogCloseStart(logger Logger, mode string) {
	if logger == nil {
		return
	}
	logger.Info("dispatcher_close_started", "mode", mode)
}

// LogCloseComplete logs the completion of dispatcher shutdown.
func LogCloseComplete(logger Logger, mode string, duration time.Duration, err error) {
	if logger == nil {
		return
	}
	fields := []interface{}{"mode", mode, "duration_ms", duration.Milliseconds()}
	if err != nil {
		fields = append(fields, "error", err.Error())
		logger.Error("dispatcher_close_completed", fields...)
	} else {
		fields = append(fields, "status", "success")
		logger.Info("dispatcher_close_completed", fields...)
	}
}

// LogJobPanic logs a panic that occurred during job execution.
func LogJobPanic(logger Logger, jobName string, recovered interface{}) {
	if logger == nil {
		return
	}
	logger.Error("job_panic", "job_name", jobName, "panic", fmt.Sprintf("%v", recovered))
}

// LogWorkerPanic logs a panic in the dispatcher worker itself.
func LogWorkerPanic(logger Logger, recovered interface{}) {
	if logger == nil {
		return
	}
	logger.Error("worker_panic", "panic", fmt.Sprintf("%v", recovered))
}
