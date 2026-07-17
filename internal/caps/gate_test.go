package caps_test

import (
	"sync"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

func TestGate_AbsentKeyFailsOpen(t *testing.T) {
	g := caps.NewGate()
	// Never Replaced: every lookup defaults to enabled.
	if !g.Allowed("appdb", caps.PgStatStatements) {
		t.Fatal("fresh gate must fail OPEN (absent key => enabled)")
	}
	// After a Replace that omits this key, it still fails open.
	g.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatActivityFullRead}: false,
	})
	if !g.Allowed("appdb", caps.PgStatStatements) {
		t.Fatal("key absent from snapshot must fail OPEN")
	}
}

func TestGate_ReplaceThenDisabled(t *testing.T) {
	g := caps.NewGate()
	g.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatStatements}:   false,
		{Db: "appdb", Cap: caps.SchemaInventory}:    true,
		{Db: "otherdb", Cap: caps.PgStatStatements}: true,
	})
	if g.Allowed("appdb", caps.PgStatStatements) {
		t.Error("appdb pg_stat_statements explicitly disabled, want false")
	}
	if !g.Allowed("appdb", caps.SchemaInventory) {
		t.Error("appdb schema_inventory explicitly enabled, want true")
	}
	// Per-db scoping: otherdb is enabled for the same capability.
	if !g.Allowed("otherdb", caps.PgStatStatements) {
		t.Error("otherdb pg_stat_statements enabled, want true")
	}
}

func TestAllowedStrict_FailClosed(t *testing.T) {
	g := caps.NewGate()
	// empty gate: fail-open Allowed is true, fail-closed AllowedStrict is false.
	if !g.Allowed("db", caps.QueryTextT2) {
		t.Fatal("Allowed should fail-open true on empty gate")
	}
	if g.AllowedStrict("db", caps.QueryTextT2) {
		t.Fatal("AllowedStrict must fail-closed false on empty gate")
	}
	g.Replace(map[caps.GateKey]bool{{Db: "db", Cap: caps.QueryTextT2}: true})
	if !g.AllowedStrict("db", caps.QueryTextT2) {
		t.Fatal("AllowedStrict must be true on explicit true")
	}
	g.Replace(map[caps.GateKey]bool{{Db: "db", Cap: caps.QueryTextT2}: false})
	if g.AllowedStrict("db", caps.QueryTextT2) {
		t.Fatal("AllowedStrict must be false on explicit false")
	}
}

func TestGate_ConcurrentReadDuringReplace(t *testing.T) {
	g := caps.NewGate()
	g.Replace(map[caps.GateKey]bool{
		{Db: "appdb", Cap: caps.PgStatStatements}: true,
	})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = g.Allowed("appdb", caps.PgStatStatements) }()
		go func() {
			defer wg.Done()
			g.Replace(map[caps.GateKey]bool{
				{Db: "appdb", Cap: caps.PgStatStatements}: false,
			})
		}()
	}
	wg.Wait()
}
