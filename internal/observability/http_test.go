package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"image-gateway-middleware/internal/access"
)

func TestLogHTTPRequestsFieldsStatusAndBytes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		handler    http.Handler
		wantStatus int
		wantBytes  int64
		wantLevel  string
	}{
		{
			name:       "implicit 200 without write",
			handler:    http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
			wantStatus: http.StatusOK,
			wantLevel:  "DEBUG",
		},
		{
			name: "implicit 200 on write",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("hello"))
			}),
			wantStatus: http.StatusOK,
			wantBytes:  5,
			wantLevel:  "DEBUG",
		},
		{
			name: "explicit status only counted once",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("created"))
			}),
			wantStatus: http.StatusCreated,
			wantBytes:  7,
			wantLevel:  "DEBUG",
		},
		{
			name: "client error",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "forbidden", http.StatusForbidden)
			}),
			wantStatus: http.StatusForbidden,
			wantBytes:  10,
			wantLevel:  "INFO",
		},
		{
			name: "server error",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}),
			wantStatus: http.StatusServiceUnavailable,
			wantLevel:  "WARN",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := NewLogger(&output, slog.LevelDebug)
			req := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/images?token=secret", nil)
			req.RemoteAddr = "192.0.2.10:4321"
			recorder := httptest.NewRecorder()

			LogHTTPRequests(tc.handler, logger, "data").ServeHTTP(recorder, req)

			record := decodeRecord(t, &output)
			assertField(t, record, "level", tc.wantLevel)
			assertField(t, record, "component", "http_request")
			assertField(t, record, "plane", "data")
			assertField(t, record, "method", http.MethodPost)
			assertField(t, record, "path", "/v1/images")
			assertNumber(t, record, "status", int64(tc.wantStatus))
			assertNumber(t, record, "bytes", tc.wantBytes)
			assertField(t, record, "remote_addr", "192.0.2.10:4321")
			if _, ok := record["duration_ms"]; !ok {
				t.Fatal("duration_ms field is missing")
			}
			if bytes.Contains(output.Bytes(), []byte("token=secret")) {
				t.Fatalf("query leaked into log: %s", output.String())
			}
		})
	}
}

func TestLogHTTPRequestsDefaultInfoVisibility(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output, slog.LevelInfo)

	LogHTTPRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), logger, "admin").ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if output.Len() != 0 {
		t.Fatalf("successful request logged at info: %s", output.String())
	}

	LogHTTPRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}), logger, "admin").ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	record := decodeRecord(t, &output)
	assertField(t, record, "level", "INFO")
	assertNumber(t, record, "status", http.StatusForbidden)
}

func TestLogHTTPRequestsCapturesOuterAllowlistRejection(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output, slog.LevelInfo)
	allowed := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("rejected request reached application handler")
	})
	handler := LogHTTPRequests(access.AllowClients(next, allowed), logger, "data")
	req := httptest.NewRequest(http.MethodGet, "/v1/models?token=secret", nil)
	req.RemoteAddr = "192.0.2.20:1234"

	handler.ServeHTTP(httptest.NewRecorder(), req)

	record := decodeRecord(t, &output)
	assertField(t, record, "level", "INFO")
	assertField(t, record, "plane", "data")
	assertField(t, record, "path", "/v1/models")
	assertNumber(t, record, "status", http.StatusForbidden)
}

func TestLogHTTPRequestsSuppressesOnlySuccessfulHealthChecks(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent, http.StatusTemporaryRedirect} {
		var output bytes.Buffer
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
		LogHTTPRequests(handler, NewLogger(&output, slog.LevelDebug), "data").ServeHTTP(
			httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_gateway/health?probe=1", nil),
		)
		if output.Len() != 0 {
			t.Fatalf("successful health status %d was logged: %s", status, output.String())
		}
	}

	var output bytes.Buffer
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	LogHTTPRequests(handler, NewLogger(&output, slog.LevelInfo), "data").ServeHTTP(
		httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_gateway/health", nil),
	)
	record := decodeRecord(t, &output)
	assertNumber(t, record, "status", http.StatusServiceUnavailable)
}

func decodeRecord(t *testing.T, output *bytes.Buffer) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record); err != nil {
		t.Fatalf("invalid JSON log %q: %v", output.String(), err)
	}
	return record
}

func assertField(t *testing.T, record map[string]any, key string, want any) {
	t.Helper()
	if got := record[key]; got != want {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
}

func assertNumber(t *testing.T, record map[string]any, key string, want int64) {
	t.Helper()
	got, ok := record[key].(float64)
	if !ok || int64(got) != want {
		t.Fatalf("%s = %#v, want %d", key, record[key], want)
	}
}
