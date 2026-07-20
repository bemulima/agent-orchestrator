package temporal

import (
	"fmt"

	"go.uber.org/zap"
)

// Logger adapts the Temporal SDK logger contract to the service zap logger.
type Logger struct {
	logger *zap.Logger
}

func NewLogger(logger *zap.Logger) Logger {
	return Logger{logger: logger}
}

func (l Logger) Debug(message string, keyValues ...interface{}) {
	l.logger.Debug(message, zapFields(keyValues)...)
}

func (l Logger) Info(message string, keyValues ...interface{}) {
	l.logger.Info(message, zapFields(keyValues)...)
}

func (l Logger) Warn(message string, keyValues ...interface{}) {
	l.logger.Warn(message, zapFields(keyValues)...)
}

func (l Logger) Error(message string, keyValues ...interface{}) {
	l.logger.Error(message, zapFields(keyValues)...)
}

func zapFields(keyValues []interface{}) []zap.Field {
	fields := make([]zap.Field, 0, (len(keyValues)+1)/2)
	for i := 0; i < len(keyValues); i += 2 {
		key := fmt.Sprint(keyValues[i])
		if i+1 >= len(keyValues) {
			fields = append(fields, zap.String(key, "missing value"))
			continue
		}
		fields = append(fields, zap.Any(key, keyValues[i+1]))
	}
	return fields
}
