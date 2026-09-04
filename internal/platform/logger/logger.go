// Package logger configures slog to write JSON to stdout and nothing else.
// In a container world, managing log files is the runtime's job, not the app's.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New takes the decision as a bool rather than an environment name.
// Translating APP_ENV into "development or not" is the job of config, in one place.
func New(level string, development bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	// In development a human reads the logs; in production a machine does.
	if development {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
