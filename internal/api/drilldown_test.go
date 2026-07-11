package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestDrilldownPage_RendersScreenForFingerprint(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/databases/orders-prod/query/3f2a")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"3f2a", "← TOP QUERIES", "RAW SAMPLE — TIER 2", `data-screen-label="Query drilldown"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("drilldown page missing %q", want)
		}
	}
	if strings.Contains(string(body), "system-ui") {
		t.Error("drilldown must not use legacy styling")
	}
}
