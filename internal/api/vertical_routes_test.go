package api_test

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// setupEmptyFleet migrates both stores and starts the server with no seeded
// clusters — the zero-rows path (and the servers.database_name dark-screen path
// for Databases).
func setupEmptyFleet(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _, _, _, _ := newVerticalFleet(t)
	return srv
}

func TestRoutes_DatabaseVerticalScreens(t *testing.T) {
	srv := setupEmptyFleet(t)
	cases := []struct{ path, wantID string }{
		{"/databases", `id="clusters-screen"`},
		{"/nodes", `id="nodes-screen"`},
		{"/databases/all", `id="databases-screen"`},
	}
	for _, c := range cases {
		html := getBody(t, srv.URL+c.path)
		if !strings.Contains(html, c.wantID) {
			t.Errorf("GET %s: missing %q", c.path, c.wantID)
		}
		// Every screen renders inside the design shell.
		if !strings.Contains(html, `class="topbar"`) {
			t.Errorf("GET %s: not wrapped in the design shell", c.path)
		}
	}
}
