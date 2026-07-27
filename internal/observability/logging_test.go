package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    slog.Level
		wantErr bool
	}{
		{name: "empty defaults to info", want: slog.LevelInfo},
		{name: "debug", raw: "debug", want: slog.LevelDebug},
		{name: "info", raw: "info", want: slog.LevelInfo},
		{name: "warn", raw: "warn", want: slog.LevelWarn},
		{name: "error", raw: "error", want: slog.LevelError},
		{name: "trimmed", raw: " warn ", want: slog.LevelWarn},
		{name: "invalid", raw: "verbose", want: slog.LevelInfo, wantErr: true},
		{name: "uppercase is invalid", raw: "WARN", want: slog.LevelInfo, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLevel(tc.raw)
			if got != tc.want {
				t.Fatalf("level = %s, want %s", got, tc.want)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, tc.wantErr)
			}
			if err != nil && (!strings.Contains(err.Error(), "LOG_LEVEL") || !strings.Contains(err.Error(), tc.raw)) {
				t.Fatalf("error %q does not identify LOG_LEVEL and its value", err)
			}
		})
	}
}

func TestNewLoggerWritesJSONAtConfiguredLevel(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output, slog.LevelWarn)

	logger.Info("hidden")
	logger.Warn("visible", "component", "test")

	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("log lines = %d, want 1: %q", len(lines), output.String())
	}
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("invalid JSON log: %v", err)
	}
	if record["level"] != "WARN" || record["msg"] != "visible" || record["component"] != "test" {
		t.Fatalf("unexpected log record: %#v", record)
	}
}
