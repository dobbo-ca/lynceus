package collector_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
)

func TestFetchPolicySnapshot_mapsServerDefaultAndOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/servers/srv-1/policy-snapshot" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"capability":"pg_stat_statements","database_name":"","enabled":false},
			{"capability":"pg_stat_statements","database_name":"appdb","enabled":true},
			{"capability":"schema_inventory","database_name":"otherdb","enabled":false}
		]`))
	}))
	defer srv.Close()

	snap, err := collector.FetchPolicySnapshot(context.Background(), srv.URL, "srv-1", "appdb")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	// Server-wide pg_stat_statements is false, but appdb override re-enables it.
	if got, ok := snap[caps.GateKey{Db: "appdb", Cap: caps.PgStatStatements}]; !ok || got != true {
		t.Errorf("appdb pg_stat_statements = (%v,%v), want (true,true) — override beats default", got, ok)
	}
	// schema_inventory override is for otherdb, NOT the collector's appdb,
	// so it must NOT enter the snapshot keyed on appdb.
	if _, ok := snap[caps.GateKey{Db: "appdb", Cap: caps.SchemaInventory}]; ok {
		t.Error("otherdb override leaked into appdb gate key")
	}
}

func TestFetchPolicySnapshot_serverDefaultOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"capability":"pg_stat_statements","database_name":"","enabled":false}]`))
	}))
	defer srv.Close()

	snap, err := collector.FetchPolicySnapshot(context.Background(), srv.URL, "srv-1", "appdb")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := snap[caps.GateKey{Db: "appdb", Cap: caps.PgStatStatements}]; got != false {
		t.Errorf("appdb pg_stat_statements = %v, want false (server default applies)", got)
	}
}
