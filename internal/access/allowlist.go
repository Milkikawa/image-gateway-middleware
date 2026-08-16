package access

import (
	"log/slog"
	"net/http"
	"net/netip"
)

type allowClientsConfig struct {
	logger *slog.Logger
	plane  string
}

// AllowClientsOption configures optional allowlist diagnostics.
type AllowClientsOption func(*allowClientsConfig)

// WithAllowlistLogger adds structured rejection logs for the named traffic plane.
func WithAllowlistLogger(logger *slog.Logger, plane string) AllowClientsOption {
	return func(config *allowClientsConfig) {
		config.logger = logger
		config.plane = plane
	}
}

// AllowClients permits requests whose direct TCP peer matches an allowed prefix.
// Forwarded client headers are intentionally ignored.
func AllowClients(next http.Handler, allowed []netip.Prefix, options ...AllowClientsOption) http.Handler {
	config := allowClientsConfig{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			logAllowlistRejection(config, r, "invalid_remote_addr", "")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		peerIP := peer.Addr().WithZone("").Unmap()
		if !contains(allowed, peerIP) {
			logAllowlistRejection(config, r, "not_allowed", peerIP.String())
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logAllowlistRejection(config allowClientsConfig, r *http.Request, reason, peerIP string) {
	if config.logger == nil {
		return
	}
	attributes := []any{
		"component", "allowlist",
		"plane", config.plane,
		"method", r.Method,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
		"reason", reason,
	}
	if peerIP != "" {
		attributes = append(attributes, "peer_ip", peerIP)
	}
	config.logger.Warn("client rejected by allowlist", attributes...)
}

func contains(allowed []netip.Prefix, addr netip.Addr) bool {
	addr = addr.WithZone("").Unmap()
	for _, prefix := range allowed {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
