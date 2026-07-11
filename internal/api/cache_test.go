package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfig_CacheEnabled(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"none", Config{}, false},
		{"redis", Config{EnableRedis: true}, true},
		{"valkey", Config{EnableValkey: true}, true},
		{"both", Config{EnableRedis: true, EnableValkey: true}, true},
	}
	for _, c := range cases {
		if got := c.cfg.CacheEnabled(); got != c.want {
			t.Errorf("%s: CacheEnabled()=%v want %v", c.name, got, c.want)
		}
	}
}

// handleCacheClusters must 404 when the cache vertical is disabled, before it
// ever touches the store — so a nil-store Server is a valid, deliberate probe.
func TestHandleCacheClusters_GatedOff(t *testing.T) {
	s := &Server{cfg: Config{}}
	req := httptest.NewRequest(http.MethodGet, "/cache/clusters", nil)
	w := httptest.NewRecorder()
	s.handleCacheClusters(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("disabled cache: got %d want 404", w.Code)
	}
}
