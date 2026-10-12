package logger

import "go.uber.org/zap"

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
