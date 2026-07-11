package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestProviderSetupPage_unselected(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/admin/provider-setup")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{"<!doctype html>", "Provider Setup", "SELECT A PROVIDER TO SEE ITS SETUP GUIDE", "/partial/provider-setup?provider=aws"} {
		if !strings.Contains(html, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestProviderSetupPartial_AWS_firehose(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/partial/provider-setup?provider=aws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, want := range []string{"PATH 3 — FIREHOSE INGESTION (CONTROLLED INGRESS)", "aws_kinesis_firehose_delivery_stream", "X-Lynceus-Tenant"} {
		if !strings.Contains(html, want) {
			t.Errorf("aws partial missing %q", want)
		}
	}
	// a fragment must NOT carry the full-page doctype
	if strings.Contains(html, "<!doctype html>") {
		t.Error("partial must be a bare fragment, not a full page")
	}
}

func TestProviderSetup_requiresDevAuth(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/admin/provider-setup")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
