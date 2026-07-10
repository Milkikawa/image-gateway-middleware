package httpdata

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

func Health(db *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		state := "ok"
		if err := db.PingContext(r.Context()); err != nil {
			status = http.StatusServiceUnavailable
			state = "unavailable"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": state})
	})
}
