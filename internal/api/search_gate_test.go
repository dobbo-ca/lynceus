package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestConfig_SearchEnabled is a pure predicate test — no server, no DB.
func TestConfig_SearchEnabled(t *testing.T) {
	for _, c := range []struct {
		os, es bool
		want   bool
	}{
		{false, false, false},
		{true, false, true},
		{false, true, true},
		{true, true, true},
	} {
		cfg := Config{EnableOpensearch: c.os, EnableElasticsearch: c.es}
		if got := cfg.SearchEnabled(); got != c.want {
			t.Errorf("Config{os:%v es:%v}.SearchEnabled() = %v, want %v", c.os, c.es, got, c.want)
		}
	}
}

// newSearchGateHandler builds a routed handler backed by NIL stores. The gate
// path returns 404 before any handler touches a store (buildShellView / fetch*
// are only reached once SearchEnabled() is true), so the disabled-route gate is
// exercised DB-free and runs even when Docker is unavailable. DevAuth is forced
// on so /search/* reaches the handler instead of the 401 auth wall. The enabled
// (200) render path needs the config store and is covered by search_page_test.go
// (testcontainers).
func newSearchGateHandler(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	cfg.DevAuth = true
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.routes()
	return s.Handler()
}

func TestSearchDomains_disabled404(t *testing.T) {
	h := newSearchGateHandler(t, Config{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/search/domains", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled /search/domains = %d, want 404", w.Code)
	}
}

func TestSearchDomainsPartial_disabled404(t *testing.T) {
	h := newSearchGateHandler(t, Config{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/partial/search/domains", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled /partial/search/domains = %d, want 404", w.Code)
	}
}

func TestSearchNodes_disabled404(t *testing.T) {
	h := newSearchGateHandler(t, Config{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/search/nodes", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled /search/nodes = %d, want 404", w.Code)
	}
}

func TestSearchNodesPartial_disabled404(t *testing.T) {
	h := newSearchGateHandler(t, Config{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/partial/search/nodes", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled /partial/search/nodes = %d, want 404", w.Code)
	}
}
