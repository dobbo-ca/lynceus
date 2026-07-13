package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
)

func TestSettingsAccessSkeletonRenders(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/settings/access")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	h := string(body)
	if !strings.Contains(h, `data-screen="settings-access"`) {
		t.Fatalf("missing screen marker; body=%s", h)
	}
	if !strings.Contains(h, "ln-nav") {
		t.Fatalf("missing shell nav; body=%s", h)
	}
}

func TestSettingsProvidersSkeletonRenders(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/settings/providers")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	h := string(body)
	if !strings.Contains(h, `data-screen="settings-providers"`) {
		t.Fatalf("missing screen marker; body=%s", h)
	}
	if !strings.Contains(h, "ln-nav") {
		t.Fatalf("missing shell nav; body=%s", h)
	}
}

func TestSettingsCollectorsSkeletonRenders(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/settings/collectors")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	h := string(body)
	if !strings.Contains(h, `data-screen="settings-collectors"`) {
		t.Fatalf("missing screen marker; body=%s", h)
	}
	if !strings.Contains(h, "ln-nav") {
		t.Fatalf("missing shell nav; body=%s", h)
	}
}

func TestSettingsRetentionSkeletonRenders(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/settings/retention")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	h := string(body)
	if !strings.Contains(h, `data-screen="settings-retention"`) {
		t.Fatalf("missing screen marker; body=%s", h)
	}
	if !strings.Contains(h, "ln-nav") {
		t.Fatalf("missing shell nav; body=%s", h)
	}
}

func TestSettingsGeneralSkeletonRenders(t *testing.T) {
	_, srv := setup(t, api.Config{DevAuth: true})
	resp, err := http.Get(srv.URL + "/settings/general")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	h := string(body)
	if !strings.Contains(h, `data-screen="settings-general"`) {
		t.Fatalf("missing screen marker; body=%s", h)
	}
	if !strings.Contains(h, "ln-nav") {
		t.Fatalf("missing shell nav; body=%s", h)
	}
}
