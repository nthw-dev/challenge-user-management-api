package logger_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/platform/logger"
)

func TestNew_Level(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level   string
		enabled []slog.Level
		muted   []slog.Level
	}{
		{"debug", []slog.Level{slog.LevelDebug, slog.LevelInfo}, nil},
		{"info", []slog.Level{slog.LevelInfo, slog.LevelWarn}, []slog.Level{slog.LevelDebug}},
		{"warn", []slog.Level{slog.LevelWarn, slog.LevelError}, []slog.Level{slog.LevelInfo}},
		{"error", []slog.Level{slog.LevelError}, []slog.Level{slog.LevelWarn}},
		{"ERROR", []slog.Level{slog.LevelError}, []slog.Level{slog.LevelWarn}},   // case does not matter
		{"", []slog.Level{slog.LevelInfo}, []slog.Level{slog.LevelDebug}},        // the default is info
		{"verbose", []slog.Level{slog.LevelInfo}, []slog.Level{slog.LevelDebug}}, // so is an unknown word
	}

	for _, tt := range tests {
		t.Run("level="+tt.level, func(t *testing.T) {
			t.Parallel()
			h := logger.New(tt.level, false).Handler()

			for _, l := range tt.enabled {
				assert.True(t, h.Enabled(context.Background(), l), "%v should be enabled", l)
			}
			for _, l := range tt.muted {
				assert.False(t, h.Enabled(context.Background(), l), "%v should be muted", l)
			}
		})
	}
}

// In development a human reads the logs; in production a machine does — the format follows the flag, nothing else.
func TestNew_Format(t *testing.T) {
	t.Parallel()

	require.IsType(t, &slog.TextHandler{}, logger.New("info", true).Handler())
	require.IsType(t, &slog.JSONHandler{}, logger.New("info", false).Handler())
}
