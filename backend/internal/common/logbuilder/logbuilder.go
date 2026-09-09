package logbuilder

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	LogBuilderInfoLevel  = "INFO"
	LogBuilderErrorLevel = "ERROR"
	LogBuilderDebugLevel = "DEBUG"
	LogBuilderTraceLevel = "TRACE"
)

type Fields map[string]any

type Config struct {
	Level         *string
	DisableColors *bool
}

type jsmLogger struct {
	logger *logrus.Logger
}

// Use this as
func NewDefaultInfoLevelLogger() *jsmLogger {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	logger.SetFormatter(&logrus.TextFormatter{
		DisableColors:   false,
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339,
	})
	return &jsmLogger{logger: logger}
}

func NewJsmLogger(config Config) (*jsmLogger, error) {
	if config.Level == nil || *config.Level == "" || config.DisableColors == nil {
		return nil, fmt.Errorf("all fields must be set")
	}

	level, err := logrus.ParseLevel(*config.Level)
	if err != nil {
		return nil, err
	}
	logger := logrus.New()
	logger.SetLevel(level)

	if *config.DisableColors {
		logger.SetFormatter(&logrus.TextFormatter{
			DisableColors:   *config.DisableColors,
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339,
		})
	}
	return &jsmLogger{logger: logger}, nil
}

func (l *jsmLogger) entry(fields ...Fields) *logrus.Entry {
	return l.logger.WithFields(
		logrus.Fields(mergeFields(fields...)),
	)
}

func mergeFields(fieldSets ...Fields) Fields {
	result := make(Fields)

	for _, fields := range fieldSets {
		for key, value := range fields {
			result[key] = value
		}
	}
	return result
}

func (l *jsmLogger) Debug(message string, fields ...Fields) {
	l.entry(fields...).Debug(message)
}

func (l *jsmLogger) Info(message string, fields ...Fields) {
	l.entry(fields...).Info(message)
}

func (l *jsmLogger) Warn(message string, fields ...Fields) {
	l.entry(fields...).Warn(message)
}

func (l *jsmLogger) Error(message string, fields ...Fields) {
	l.entry(fields...).Error(message)
}

func (l *jsmLogger) Fatal(message string, fields ...Fields) {
	l.entry(fields...).Fatal(message)
}

func (l *jsmLogger) Track(operation string, fields ...Fields) func() {
	start := time.Now()
	l.Debug(
		"operation started",
		mergeWithAdditional(fields, Fields{
			"operation": operation,
		}),
	)

	return func() {
		duration := time.Since(start)
		l.logger.Info(
			"operation completed",
			mergeWithAdditional(fields, Fields{
				"operation":   operation,
				"duration":    duration.String(),
				"duration_ms": duration.Milliseconds(),
			}),
		)
	}
}

func mergeWithAdditional(existing []Fields, additional Fields) Fields {
	fieldSets := make([]Fields, 0, len(existing)+1)
	fieldSets = append(fieldSets, existing...)
	fieldSets = append(fieldSets, additional)

	return mergeFields(fieldSets...)
}
