// Package logger provides structured logging for the gokue job queue.
package logger

import (
	"io"

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
