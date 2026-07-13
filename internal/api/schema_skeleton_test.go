package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestSchemaInventorySkeletonRenders(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/schema/inventory")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	h := string(body)
	if !strings.Contains(h, `data-screen="inventory"`) {
		t.Fatalf("missing screen marker; body=%s", h)
	}
	if !strings.Contains(h, "ln-nav") {
		t.Fatalf("missing shell nav; body=%s", h)
	}
}

func TestSchemaTableGrowthSkeletonRenders(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/schema/table-growth")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	h := string(body)
	if !strings.Contains(h, `data-screen="tablegrowth"`) {
		t.Fatalf("missing screen marker; body=%s", h)
	}
	if !strings.Contains(h, "ln-nav") {
		t.Fatalf("missing shell nav; body=%s", h)
	}
}

func TestSchemaIndexesSkeletonRenders(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/schema/indexes")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	h := string(body)
	if !strings.Contains(h, `data-screen="indexes"`) {
		t.Fatalf("missing screen marker; body=%s", h)
	}
	if !strings.Contains(h, "ln-nav") {
		t.Fatalf("missing shell nav; body=%s", h)
	}
}
