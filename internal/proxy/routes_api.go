package proxy

import (
	"encoding/json"
	"net/http"
)

// RoutesAPIHandler returns a JSON map of routes (host -> upstream).
// Useful for debugging / admin UI.
func RoutesAPIHandler(m *ShardedRouteManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := m.ListRoutes()
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	}
}
