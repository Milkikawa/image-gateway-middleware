package httpdata

import (
	"encoding/json"
	"net/http"
	"strings"
)

type Router struct {
	proxy  *Proxy
	image  http.Handler
	health http.Handler
}

func NewRouter(proxy *Proxy, image, health http.Handler) http.Handler {
	return &Router{proxy: proxy, image: image, health: health}
}
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/v1/models":
		if req.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		r.proxy.Models(w, req)
	case "/v1/images/generations":
		if req.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		r.proxy.Image(w, req, false)
	case "/v1/images/edits":
		if req.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		r.proxy.Image(w, req, true)
	case "/_gateway/health":
		if req.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		r.health.ServeHTTP(w, req)
	default:
		if strings.HasPrefix(req.URL.Path, "/_gateway/images/") {
			if req.Method != http.MethodGet && req.Method != http.MethodHead {
				methodNotAllowed(w, http.MethodGet, http.MethodHead)
				return
			}
			r.image.ServeHTTP(w, req)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}
func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
