package observability

import (
	"log/slog"
	"net/http"
	"time"
)

// LogHTTPRequests records one structured completion event for each HTTP request.
func LogHTTPRequests(next http.Handler, logger *slog.Logger, plane string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		response := &responseWriter{ResponseWriter: w}

		next.ServeHTTP(response, r)

		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		if r.Method == http.MethodGet && r.URL.Path == "/_gateway/health" && status >= 200 && status < 400 {
			return
		}

		level := slog.LevelDebug
		switch {
		case status >= 500:
			level = slog.LevelWarn
		case status >= 400:
			level = slog.LevelInfo
		}
		logger.Log(r.Context(), level, "HTTP request completed",
			"component", "http_request",
			"plane", plane,
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(started).Milliseconds(),
			"bytes", response.bytes,
			"remote_addr", r.RemoteAddr,
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

// Unwrap allows net/http.ResponseController to reach optional capabilities of the original writer.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
