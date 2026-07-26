package access

import (
	"net/http"
	"net/netip"
)

// AllowClients permits requests whose direct TCP peer matches an allowed prefix.
// Forwarded client headers are intentionally ignored.
func AllowClients(next http.Handler, allowed []netip.Prefix) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil || !contains(allowed, peer.Addr()) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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
