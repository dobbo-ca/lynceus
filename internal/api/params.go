package api

import "net/http"

// q1 returns query param key or def when absent/empty.
func q1(r *http.Request, key, def string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return v
	}
	return def
}
