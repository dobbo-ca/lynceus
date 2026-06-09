package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// capabilityCell mirrors the JSON the matrix endpoint returns.
type capabilityCell struct {
	Capability       string `json:"capability"`
	DatabaseName     string `json:"database_name"`
	DiscoveredAvail  bool   `json:"discovered_available"`
	DiscoveredReason string `json:"discovered_reason"`
	PolicyEnabled    *bool  `json:"policy_enabled"`
	PolicySource     string `json:"policy_source"`
	FinalEnabled     bool   `json:"final_enabled"`
}
type capabilityMatrix struct {
	ServerID string           `json:"server_id"`
	Cells    []capabilityCell `json:"cells"`
}

func seedServer(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO servers (id, name) VALUES ($1, $1)`, id); err != nil {
		t.Fatalf("seed server: %v", err)
	}
}

func cellByCap(cells []capabilityCell, cap string) (capabilityCell, bool) {
	for _, c := range cells {
		if c.Capability == cap {
			return c, true
		}
	}
	return capabilityCell{}, false
}

func TestCapabilityMatrix_joinsDiscoveredAndPolicy(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedServer(t, pool, "srv-1")
	ctx := context.Background()

	// pg_stat_statements: discovered=true, policy=disabled => final false.
	if _, err := pool.Exec(ctx,
		`INSERT INTO discovered_capability (server_id, database_name, capability, available, reason)
		 VALUES ('srv-1', NULL, 'pg_stat_statements', true, '1.10')`); err != nil {
		t.Fatalf("seed discovered: %v", err)
	}
	if _, err := store.NewConfig(pool).SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements", Enabled: false, SetBy: "alice",
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	// auto_explain: discovered=true, NO policy row => policy_enabled nil, final follows discovered (true).
	if _, err := pool.Exec(ctx,
		`INSERT INTO discovered_capability (server_id, database_name, capability, available, reason)
		 VALUES ('srv-1', NULL, 'auto_explain', true, '')`); err != nil {
		t.Fatalf("seed discovered #2: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/servers/srv-1/capabilities")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var got capabilityMatrix
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ServerID != "srv-1" {
		t.Errorf("server_id = %q, want srv-1", got.ServerID)
	}

	pss, ok := cellByCap(got.Cells, "pg_stat_statements")
	if !ok {
		t.Fatal("pg_stat_statements cell missing")
	}
	if !pss.DiscoveredAvail {
		t.Error("pg_stat_statements should be discovered-available")
	}
	if pss.PolicyEnabled == nil || *pss.PolicyEnabled {
		t.Errorf("pg_stat_statements policy_enabled = %v, want non-nil false", pss.PolicyEnabled)
	}
	if pss.PolicySource != string(store.PolicySourceServerDefault) {
		t.Errorf("pg_stat_statements policy_source = %q, want server-default", pss.PolicySource)
	}
	if pss.FinalEnabled {
		t.Error("pg_stat_statements final_enabled should be false (discovered && policy-off)")
	}

	ae, ok := cellByCap(got.Cells, "auto_explain")
	if !ok {
		t.Fatal("auto_explain cell missing")
	}
	if ae.PolicyEnabled != nil {
		t.Errorf("auto_explain policy_enabled = %v, want nil (no policy row)", ae.PolicyEnabled)
	}
	if ae.PolicySource != "" {
		t.Errorf("auto_explain policy_source = %q, want empty", ae.PolicySource)
	}
	if !ae.FinalEnabled {
		t.Error("auto_explain final_enabled should be true (discovered && absent policy => enabled)")
	}
}

func TestCapabilityMatrix_absentPolicyDefaultsEnabled(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedServer(t, pool, "srv-1")
	// Discovered-available, no policy row anywhere.
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO discovered_capability (server_id, database_name, capability, available, reason)
		 VALUES ('srv-1', NULL, 'pg_buffercache', true, 'installed')`); err != nil {
		t.Fatalf("seed discovered: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/servers/srv-1/capabilities")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var got capabilityMatrix
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	c, ok := cellByCap(got.Cells, "pg_buffercache")
	if !ok {
		t.Fatal("pg_buffercache cell missing")
	}
	if c.PolicyEnabled != nil {
		t.Errorf("policy_enabled = %v, want nil", c.PolicyEnabled)
	}
	if !c.FinalEnabled {
		t.Error("final_enabled should be true when discovered and no policy (default enabled)")
	}
}

func TestCapabilityToggle_devAuth_writesPolicyAndAudit(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	seedServer(t, pool, "srv-1")
	ctx := context.Background()

	body := strings.NewReader(`{"database":"","enabled":true,"reason":"operator enabled"}`)
	resp, err := http.Post(
		srv.URL+"/api/servers/srv-1/capabilities/pg_stat_statements",
		"application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// A capability_policy row exists, enabled, for the server-wide default.
	var enabled bool
	if err := pool.QueryRow(ctx,
		`SELECT enabled FROM capability_policy
		   WHERE server_id='srv-1' AND database_name IS NULL AND capability='pg_stat_statements'`,
	).Scan(&enabled); err != nil {
		t.Fatalf("policy row missing: %v", err)
	}
	if !enabled {
		t.Error("policy row should be enabled")
	}

	// Exactly one audit row for the toggle, with the dev-admin actor.
	var n int
	var actor string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(actor),'') FROM audit_log
		   WHERE action='capability_policy.set' AND server_id='srv-1'`,
	).Scan(&n, &actor); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
	if actor != "dev-admin" {
		t.Errorf("audit actor = %q, want dev-admin", actor)
	}
}

func TestCapabilityToggle_withoutDevAuth_returns401(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: false})
	seedServer(t, pool, "srv-1")

	body := bytes.NewReader([]byte(`{"enabled":true}`))
	resp, err := http.Post(
		srv.URL+"/api/servers/srv-1/capabilities/pg_stat_statements",
		"application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}

	// No policy row was written (request rejected before the handler).
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM capability_policy WHERE server_id='srv-1'`,
	).Scan(&n)
	if n != 0 {
		t.Errorf("policy rows = %d, want 0 (toggle blocked by auth)", n)
	}
}

func TestPolicySnapshot_returnsEnabledFlagsPerCapability(t *testing.T) {
	pool, srv := setupAudit(t, api.Config{DevAuth: true})
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`INSERT INTO servers (id, name) VALUES ('srv-1', 'srv one')`,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	cfg := store.NewConfig(pool)
	// Server-wide default disabled for pg_stat_statements.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", Capability: "pg_stat_statements",
		Enabled: false, SetBy: "alice",
	}); err != nil {
		t.Fatalf("set default: %v", err)
	}
	// Per-db override enabling schema_inventory on appdb.
	if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: "srv-1", DatabaseName: "appdb", Capability: "schema_inventory",
		Enabled: true, SetBy: "bob",
	}); err != nil {
		t.Fatalf("set override: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/servers/srv-1/policy-snapshot")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var got []struct {
		Capability   string `json:"capability"`
		DatabaseName string `json:"database_name"`
		Enabled      bool   `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2 (%+v)", len(got), got)
	}

	type key struct {
		cap, db string
		on      bool
	}
	seen := map[key]bool{}
	for _, e := range got {
		seen[key{e.Capability, e.DatabaseName, e.Enabled}] = true
	}
	if !seen[key{"pg_stat_statements", "", false}] {
		t.Errorf("missing server-wide pg_stat_statements=false; got %+v", got)
	}
	if !seen[key{"schema_inventory", "appdb", true}] {
		t.Errorf("missing appdb schema_inventory=true; got %+v", got)
	}
}

func TestPolicySnapshot_withoutDevAuth_returns401(t *testing.T) {
	_, srv := setupAudit(t, api.Config{DevAuth: false})
	resp, err := http.Get(srv.URL + "/api/servers/srv-1/policy-snapshot")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
