package logger

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Fields map[string]interface{}

func Init(service, version, level string, pretty bool) {
	zerolog.TimeFieldFormat = time.RFC3339

	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)

	var writer io.Writer = os.Stdout
	if pretty {
		writer = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "15:04:05"}
	}

	log.Logger = zerolog.New(writer).
		With().
		Timestamp().
		Str("service", service).
		Str("version", version).
		Logger()
}

func With(fields Fields) zerolog.Logger {
	ctx := log.With()
	for k, v := range fields {
		ctx = ctx.Interface(k, v)
	}
	return ctx.Logger()
}

func FromContext() zerolog.Logger {
	return log.Logger
}

func NewNoop() zerolog.Logger {
	return zerolog.Nop()
}
