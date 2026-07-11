package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestAddComponentPartial_AWS(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/partial/add?kind=database&provider=aws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{
		`id="add-modal"`,
		"LYNCEUS_COLLECTOR_TOKEN",
		"ADD DATABASE CLUSTER",
		"/admin/provider-setup?provider=aws",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("add partial missing %q", want)
		}
	}
}

func TestModalClose_returnsEmpty(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/partial/modal/close")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "" {
		t.Errorf("close body = %q, want empty", string(body))
	}
}
