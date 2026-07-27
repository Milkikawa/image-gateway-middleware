package access

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"image-gateway-middleware/internal/observability"
)

func TestAllowClients(t *testing.T) {
	allowed := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("192.168.1.21/32"),
		netip.MustParsePrefix("10.20.30.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}

	for _, tc := range []struct {
		name       string
		remoteAddr string
		wantStatus int
		wantCalled bool
	}{
		{name: "exact IPv4", remoteAddr: "192.168.1.21:1234", wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "IPv4 CIDR", remoteAddr: "10.20.30.99:1234", wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "IPv4 mapped", remoteAddr: "[::ffff:10.20.30.99]:1234", wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "IPv6 CIDR", remoteAddr: "[2001:db8::42]:1234", wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "IPv4 loopback", remoteAddr: "127.0.0.1:1234", wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "IPv6 loopback", remoteAddr: "[::1]:1234", wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "outside CIDR", remoteAddr: "10.20.31.1:1234", wantStatus: http.StatusForbidden},
		{name: "unlisted", remoteAddr: "192.168.1.22:1234", wantStatus: http.StatusForbidden},
		{name: "missing port", remoteAddr: "192.168.1.21", wantStatus: http.StatusForbidden},
		{name: "invalid", remoteAddr: "not-an-address", wantStatus: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			recorder := httptest.NewRecorder()

			AllowClients(next, allowed).ServeHTTP(recorder, req)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
			if called != tc.wantCalled {
				t.Fatalf("handler called = %t, want %t", called, tc.wantCalled)
			}
		})
	}
}

func TestAllowClientsIgnoresForwardedHeaders(t *testing.T) {
	allowed := []netip.Prefix{netip.MustParsePrefix("192.168.1.21/32")}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	req.Header.Set("X-Forwarded-For", "192.168.1.21")
	req.Header.Set("X-Real-IP", "192.168.1.21")
	recorder := httptest.NewRecorder()

	AllowClients(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not be called")
	}), allowed).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestAllowClientsLogsSafeRejectionDetails(t *testing.T) {
	var output bytes.Buffer
	logger := observability.NewLogger(&output, slog.LevelInfo)
	allowed := []netip.Prefix{netip.MustParsePrefix("192.168.1.21/32")}
	req := httptest.NewRequest(http.MethodGet, "/v1/models?token=query-secret", nil)
	req.RemoteAddr = "[::ffff:203.0.113.10]:1234"
	req.Header.Set("Authorization", "Bearer authorization-secret")
	req.Header.Set("X-Forwarded-For", "forwarded-secret")
	recorder := httptest.NewRecorder()

	AllowClients(http.NotFoundHandler(), allowed, WithAllowlistLogger(logger, "data")).ServeHTTP(recorder, req)

	logOutput := output.String()
	for _, expected := range []string{`"component":"allowlist"`, `"plane":"data"`, `"reason":"not_allowed"`, `"peer_ip":"203.0.113.10"`, `"path":"/v1/models"`} {
		if !strings.Contains(logOutput, expected) {
			t.Errorf("log missing %s: %s", expected, logOutput)
		}
	}
	for _, secret := range []string{"query-secret", "authorization-secret", "forwarded-secret", "192.168.1.21/32"} {
		if strings.Contains(logOutput, secret) {
			t.Errorf("log leaked %q: %s", secret, logOutput)
		}
	}

	output.Reset()
	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "invalid-remote"
	AllowClients(http.NotFoundHandler(), allowed, WithAllowlistLogger(logger, "admin")).ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(output.String(), `"reason":"invalid_remote_addr"`) {
		t.Fatalf("invalid remote address reason missing: %s", output.String())
	}
}
