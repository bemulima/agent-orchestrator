package log

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Logger = zerolog.Logger

type Fields map[string]interface{}

func New(env string) Logger {
	level := zerolog.InfoLevel
	if env == "local" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	}
	return log.Level(level)
}

func With(logger Logger, fields Fields) Logger {
	event := logger
	for k, v := range fields {
		event = event.With().Interface(k, v).Logger()
	}
	return event
}
