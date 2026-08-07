// Package logging provides structured logging for the gokue job queue.
package logging

import (
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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

// zapLogger implements Logger using a zap sugared logger.
type zapLogger struct {
	sugar *zap.SugaredLogger
}

// Log implements Logger.
func (l *zapLogger) Log(level Level, message string, fields ...interface{}) {
	switch level {
	case LevelDebug:
		l.sugar.Debugw(message, fields...)
	case LevelInfo:
		l.sugar.Infow(message, fields...)
	case LevelWarn:
		l.sugar.Warnw(message, fields...)
	case LevelError:
		l.sugar.Errorw(message, fields...)
	}
}

// Debug implements Logger.
func (l *zapLogger) Debug(message string, fields ...interface{}) { l.sugar.Debugw(message, fields...) }

// Info implements Logger.
func (l *zapLogger) Info(message string, fields ...interface{}) { l.sugar.Infow(message, fields...) }

// Warn implements Logger.
func (l *zapLogger) Warn(message string, fields ...interface{}) { l.sugar.Warnw(message, fields...) }

// Error implements Logger.
func (l *zapLogger) Error(message string, fields ...interface{}) { l.sugar.Errorw(message, fields...) }

// NewLogger creates a structured logger that writes to w as JSON using zap,
// along with a closer that flushes the logger and closes w when it implements
// io.Closer. Call the closer (typically via defer) to release the write stream.
// The logger is safe for concurrent use; writes are serialized with a mutex.
func NewLogger(w io.Writer) (Logger, func() error) {
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.Lock(zapcore.AddSync(w)),
		zapcore.DebugLevel,
	)
	sugar := zap.New(core).Sugar()

	closer := func() error {
		if err := sugar.Sync(); err != nil {
			return err
		}
		if c, ok := w.(io.Closer); ok {
			return c.Close()
		}
		return nil
	}

	return &zapLogger{sugar: sugar}, closer
}

// LogSubmitEnqueued logs a successful job submission.
func LogSubmitEnqueued(logger Logger, jobName string) {
	if logger == nil {
		return
	}
	logger.Debug(string(EventSubmitEnqueued),
		string(FieldJobName), jobName,
		string(FieldStatus), string(StatusEnqueued))
}

// LogSubmitRejected logs a rejected job submission.
func LogSubmitRejected(logger Logger, jobName string, reason Reason) {
	if logger == nil {
		return
	}
	logger.Warn(string(EventSubmitRejected),
		string(FieldJobName), jobName,
		string(FieldReason), string(reason))
}

// LogJobProcessing logs the start of job execution.
func LogJobProcessing(logger Logger, jobName string, attempt int) {
	if logger == nil {
		return
	}
	logger.Debug(string(EventJobProcessing),
		string(FieldJobName), jobName,
		string(FieldAttempt), attempt)
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
	logger.Info(string(EventJobRetry),
		string(FieldJobName), jobName,
		string(FieldAttempt), attempt,
		string(FieldError), errStr,
		string(FieldRetryDelayMs), delay.Milliseconds())
}

// LogJobSuccess logs successful job completion.
func LogJobSuccess(logger Logger, jobName string, duration time.Duration) {
	if logger == nil {
		return
	}
	logger.Info(string(EventJobCompleted),
		string(FieldJobName), jobName,
		string(FieldStatus), string(StatusSuccess),
		string(FieldDurationMs), duration.Milliseconds())
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
	logger.Error(string(EventJobFailed),
		string(FieldJobName), jobName,
		string(FieldAttempt), attempt,
		string(FieldError), errStr,
		string(FieldDurationMs), duration.Milliseconds(),
		string(FieldStatus), string(StatusFinalFailure))
}

// LogJobAbandoned logs a job abandoned during dispatcher shutdown.
func LogJobAbandoned(logger Logger, jobName string, attempt int, reason Reason, duration time.Duration) {
	if logger == nil {
		return
	}
	logger.Warn(string(EventJobAbandoned),
		string(FieldJobName), jobName,
		string(FieldAttempt), attempt,
		string(FieldReason), string(reason),
		string(FieldDurationMs), duration.Milliseconds())
}

// LogCloseStart logs the start of dispatcher shutdown.
func LogCloseStart(logger Logger, mode string) {
	if logger == nil {
		return
	}
	logger.Info(string(EventCloseStarted), string(FieldMode), mode)
}

// LogCloseComplete logs the completion of dispatcher shutdown.
func LogCloseComplete(logger Logger, mode string, duration time.Duration, err error) {
	if logger == nil {
		return
	}
	fields := []interface{}{string(FieldMode), mode, string(FieldDurationMs), duration.Milliseconds()}
	if err != nil {
		fields = append(fields, string(FieldError), err.Error())
		logger.Error(string(EventCloseCompleted), fields...)
	} else {
		fields = append(fields, string(FieldStatus), string(StatusSuccess))
		logger.Info(string(EventCloseCompleted), fields...)
	}
}

// LogJobPanic logs a panic that occurred during job execution.
func LogJobPanic(logger Logger, jobName string, recovered interface{}) {
	if logger == nil {
		return
	}
	logger.Error(string(EventJobPanic),
		string(FieldJobName), jobName,
		string(FieldPanic), fmt.Sprintf("%v", recovered))
}

// LogWorkerPanic logs a panic in the dispatcher worker itself.
func LogWorkerPanic(logger Logger, recovered interface{}) {
	if logger == nil {
		return
	}
	logger.Error(string(EventWorkerPanic), string(FieldPanic), fmt.Sprintf("%v", recovered))
}
