package observability

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// ParseLevel parses LOG_LEVEL. An empty value uses the info level.
func ParseLevel(raw string) (slog.Level, error) {
	switch strings.TrimSpace(raw) {
	case "":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, or error; got %q", raw)
	}
}

// NewLogger creates a JSON logger that writes to w at the configured minimum level.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}
