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

// TestAddButtons_wireToModalRoot asserts the vertical "+ ADD" call-to-actions
// are HTMX triggers into the shell's #modal-root (not the dead /onboarding
// link), and that GET /partial/add?kind=database serves the wizard the trigger
// targets (ly-ae6.12 reconcile).
func TestAddButtons_wireToModalRoot(t *testing.T) {
	srv := setupEmptyFleet(t)

	// The design shell always renders the modal target.
	// The Clusters "+ ADD CLUSTER" button is an HTMX trigger into it.
	dbHTML := getBody(t, srv.URL+"/databases")
	for _, want := range []string{
		`id="modal-root"`,
		`hx-get="/partial/add?kind=database"`,
		`hx-target="#modal-root"`,
	} {
		if !strings.Contains(dbHTML, want) {
			t.Errorf("/databases missing %q", want)
		}
	}
	if strings.Contains(dbHTML, "/onboarding?") {
		t.Error("/databases still renders the dead /onboarding link")
	}

	// The trigger resolves to a real 200 wizard fragment.
	resp, err := http.Get(srv.URL + "/partial/add?kind=database")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{`id="add-modal"`, "ADD DATABASE CLUSTER"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/partial/add?kind=database missing %q", want)
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
