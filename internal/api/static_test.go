package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Static assets must be reachable even when DevAuth is off (unauthenticated),
// so login/error pages can style themselves. Everything else 401s.
func TestStatic_BypassesAuth(t *testing.T) {
	s := &Server{cfg: Config{DevAuth: false}, mux: http.NewServeMux()}
	s.routes()

	req := httptest.NewRequest(http.MethodGet, "/static/css/tokens.css", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Fatal("static asset was 401'd — it must bypass auth")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/css/tokens.css = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "--acc:#2dd4bf") {
		t.Error("served body is not tokens.css")
	}
}
