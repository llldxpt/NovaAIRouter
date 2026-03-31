package logger

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

var (
	log zerolog.Logger
)

func init() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
}

func New(level string, writers ...io.Writer) zerolog.Logger {
	zerolog.SetGlobalLevel(parseLevel(level))

	var output io.Writer = os.Stdout
	if len(writers) > 0 && writers[0] != nil {
		output = writers[0]
	}

	log = zerolog.New(output).With().Timestamp().Caller().Logger()
	return log.With().Caller().Logger()
}

func parseLevel(level string) zerolog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}

func SetLevel(level string) {
	zerolog.SetGlobalLevel(parseLevel(level))
}

func Debug() *zerolog.Event {
	return log.Debug()
}

func Info() *zerolog.Event {
	return log.Info()
}

func Warn() *zerolog.Event {
	return log.Warn()
}

func Error() *zerolog.Event {
	return log.Error()
}

func Log() *zerolog.Logger {
	return &log
}
