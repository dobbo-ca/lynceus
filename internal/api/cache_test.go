package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dobbo-ca/lynceus/web"
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

func TestSortCacheReplicasets(t *testing.T) {
	rows := []web.CacheReplicasetRow{
		{Name: "b", SevRank: 0}, {Name: "a", SevRank: 2}, {Name: "c", SevRank: 1},
	}
	sortCacheReplicasets(rows, "health") // worst (highest SevRank) first
	if rows[0].Name != "a" || rows[1].Name != "c" || rows[2].Name != "b" {
		t.Errorf("health sort order = %v %v %v", rows[0].Name, rows[1].Name, rows[2].Name)
	}
	sortCacheReplicasets(rows, "name")
	if rows[0].Name != "a" || rows[1].Name != "b" || rows[2].Name != "c" {
		t.Errorf("name sort order = %v %v %v", rows[0].Name, rows[1].Name, rows[2].Name)
	}
}

func TestSortCacheNodes(t *testing.T) {
	rows := []web.CacheNodeRow{
		{Name: "x", OpsVal: 10}, {Name: "y", OpsVal: 30}, {Name: "z", OpsVal: 20},
	}
	sortCacheNodes(rows, "ops") // highest OPS first
	if rows[0].Name != "y" || rows[1].Name != "z" || rows[2].Name != "x" {
		t.Errorf("ops sort order = %v %v %v", rows[0].Name, rows[1].Name, rows[2].Name)
	}
	sortCacheNodes(rows, "name")
	if rows[0].Name != "x" || rows[2].Name != "z" {
		t.Errorf("name sort order = %v .. %v", rows[0].Name, rows[2].Name)
	}
}

func TestFetchCacheReplicasets_SortDefault(t *testing.T) {
	s := &Server{cfg: Config{EnableRedis: true}}
	req := httptest.NewRequest(http.MethodGet, "/cache/replicasets", nil)
	if v := s.fetchCacheReplicasets(req); v.Sort != "health" {
		t.Errorf("default sort = %q want health", v.Sort)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/cache/replicasets?sort=name", nil)
	if v := s.fetchCacheReplicasets(req2); v.Sort != "name" {
		t.Errorf("explicit sort = %q want name", v.Sort)
	}
}

func TestFetchCacheNodes_SortDefault(t *testing.T) {
	s := &Server{cfg: Config{EnableRedis: true}}
	req := httptest.NewRequest(http.MethodGet, "/cache/nodes", nil)
	if v := s.fetchCacheNodes(req); v.Sort != "ops" {
		t.Errorf("default sort = %q want ops", v.Sort)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/cache/nodes?sort=name", nil)
	if v := s.fetchCacheNodes(req2); v.Sort != "name" {
		t.Errorf("explicit sort = %q want name", v.Sort)
	}
}

func TestHandleCacheReplicasetsAndNodes_GatedOff(t *testing.T) {
	s := &Server{cfg: Config{}}
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
		path string
	}{
		{"replicasets", s.handleCacheReplicasets, "/cache/replicasets"},
		{"nodes", s.handleCacheNodes, "/cache/nodes"},
	} {
		w := httptest.NewRecorder()
		tc.h(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s gated off: got %d want 404", tc.name, w.Code)
		}
	}
}
