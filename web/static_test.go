package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticHandler_ServesTokensCSS(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/css/tokens.css", nil)
	rec := httptest.NewRecorder()
	StaticHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET tokens.css = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"--acc:#2dd4bf",        // dark default accent
		"[data-theme='light']", // light override block
		"--acc:#0d9488",        // light accent
		"--radius:2px",         // shape token
		"@font-face",           // fonts declared here
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tokens.css missing %q", want)
		}
	}
}

func TestStaticHandler_ServesFonts(t *testing.T) {
	for _, path := range []string{
		"/static/fonts/work-sans-latin.woff2",
		"/static/fonts/jetbrains-mono-latin.woff2",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		StaticHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (font not vendored?)", path, rec.Code)
		}
		if rec.Body.Len() < 1000 {
			t.Errorf("GET %s served %d bytes, want a real woff2", path, rec.Body.Len())
		}
	}
}

func TestStaticHandler_ServesHTMX(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/js/htmx.min.js", nil)
	rec := httptest.NewRecorder()
	StaticHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET htmx.min.js = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "htmx") {
		t.Error("served file does not look like htmx")
	}
}

func TestStaticHandler_ServesThemeJSAndLegacyCSS(t *testing.T) {
	cases := []struct {
		path, contains string
	}{
		{"/static/js/theme.js", "setTheme"},
		{"/static/js/theme.js", "cycleTheme"},
		{"/static/js/theme.js", "setAccent"},
		{"/static/css/legacy.css", ".db-card"},
		{"/static/css/scope.css", ".scope-screen"},
		{"/static/css/governance.css", ".audit-row--t2"},
		{"/static/css/governance.css", ".chain-banner"},
		{"/static/js/settings.js", "setAccent"},
		{"/static/js/settings.js", "data-accent"},
		{"/static/js/settings.js", "lynceus.accent"},
		{"/static/css/screens.css", ".screen-hd"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		rec := httptest.NewRecorder()
		StaticHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", c.path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), c.contains) {
			t.Errorf("GET %s missing %q", c.path, c.contains)
		}
	}
}
