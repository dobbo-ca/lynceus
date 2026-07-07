// Integration tests for the T2 literal-read gateway (ly-06i). Real
// Postgres via testcontainers — two pools (config DB + stats DB), never
// mocked, because the cross-DB coupling (config-DB gate + audit chain vs
// stats-DB literal SELECT) and the fail-closed ordering are exactly what
// we are validating.
package store_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// t2Fixture spins a config pool and a stats pool, migrates both, seeds a
// servers row (t2_enabled per arg), optionally an enabled capability
// policy, and writes one data_tier=2 query_stats row for readback.
type t2Fixture struct {
	cfg      *pgxpool.Pool
	stats    *pgxpool.Pool
	reader   *store.T2Reader
	serverID string
	capName  string
	dbName   string
	when     time.Time
}

func newT2Fixture(t *testing.T, t2Enabled, capEnabled bool) t2Fixture {
	t.Helper()
	ctx := context.Background()
	cfgPool := newPool(t)
	statsPool := newPool(t)
	if err := store.ApplyConfigMigrations(ctx, cfgPool); err != nil {
		t.Fatalf("migrate config: %v", err)
	}
	if err := store.ApplyStatsMigrations(ctx, statsPool); err != nil {
		t.Fatalf("migrate stats: %v", err)
	}

	f := t2Fixture{
		cfg:      cfgPool,
		stats:    statsPool,
		serverID: "srv-1",
		capName:  "query_text",
		dbName:   "app",
		when:     time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
	}

	if _, err := cfgPool.Exec(ctx,
		`INSERT INTO servers (id, name, t2_enabled) VALUES ($1, 'srv one', $2)`,
		f.serverID, t2Enabled); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	cfg := store.NewConfig(cfgPool)
	if capEnabled {
		if _, err := cfg.SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
			ServerID:   f.serverID,
			Capability: f.capName,
			Enabled:    true,
			SetBy:      "admin",
			Reason:     "test",
		}); err != nil {
			t.Fatalf("set capability: %v", err)
		}
	}

	// One literal-capable T2 row to read back.
	s := store.NewStats(statsPool)
	if err := s.WriteQueryStats(ctx, []store.QueryStat{{
		ServerID: f.serverID, CollectedAt: f.when,
		Fingerprint: "fp-secret", NormalizedQuery: "SELECT * FROM t WHERE ssn = '123-45-6789'",
		DataTier: 2, Calls: 1, TotalTimeMs: 1,
	}}); err != nil {
		t.Fatalf("write t2 row: %v", err)
	}

	f.reader = store.NewT2Reader(cfg, s)
	return f
}

func (f *t2Fixture) request(actor string) store.T2ReadRequest {
	return store.T2ReadRequest{
		ServerID:     f.serverID,
		DatabaseName: f.dbName,
		Capability:   f.capName,
		Actor:        actor,
		Since:        f.when.Add(-time.Hour),
		Until:        f.when.Add(time.Hour),
		Limit:        10,
	}
}

func (f *t2Fixture) tier2AuditCount(t *testing.T) int {
	t.Helper()
	two := int16(2)
	recs, err := store.NewConfig(f.cfg).ListAudit(context.Background(),
		store.AuditFilter{Action: "read", Tier: &two, Limit: 1000})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	return len(recs)
}

// FAIL-CLOSED (headline): t2_enabled=true, authorized, a T2 row exists,
// but the audit append is forced to fail while steps 1 (ServerT2Enabled)
// and 2 (EffectiveCapability) still SUCCEED. We drop ONLY the audit_log
// table: the servers and capability_policy reads are untouched, so the
// gate resolves ALLOW and execution reaches the audit append (step 3),
// which errors — exercising the exact fail-closed guard. The read MUST
// return a non-nil error routed through the audit path AND ZERO literal
// rows. This test fails if the guard is removed OR the SELECT is
// reordered before the append.
func TestT2Read_FailsClosed_whenAuditAppendFails(t *testing.T) {
	f := newT2Fixture(t, true, true)
	ctx := context.Background()

	// Precondition: the gate would otherwise ALLOW — steps 1 and 2 pass.
	if enabled, found, err := store.NewConfig(f.cfg).ServerT2Enabled(ctx, f.serverID); err != nil || !found || !enabled {
		t.Fatalf("precondition: ServerT2Enabled = (%v,%v,%v), want (true,true,nil)", enabled, found, err)
	}
	if enabled, _, found, err := store.NewConfig(f.cfg).EffectiveCapability(ctx, f.serverID, f.dbName, f.capName); err != nil || !found || !enabled {
		t.Fatalf("precondition: EffectiveCapability = (%v,%v,%v), want (true,true,nil)", enabled, found, err)
	}

	// Targeted seam: drop ONLY audit_log so AppendAuditReturning errors
	// while the servers + capability_policy gate reads keep succeeding.
	// CASCADE drops the capability_policy.audit_chain_id FK constraint but
	// leaves the capability_policy table (and its rows) intact, so step 2
	// still resolves ALLOW.
	if _, err := f.cfg.Exec(ctx, `DROP TABLE audit_log CASCADE`); err != nil {
		t.Fatalf("drop audit_log: %v", err)
	}

	rows, err := f.reader.ReadT2QueryStats(ctx, f.request("alice"))
	if err == nil {
		t.Fatal("expected error when audit append fails, got nil (literal leak)")
	}
	if !strings.Contains(err.Error(), "t2 audit:") {
		t.Fatalf("expected audit-path error (t2 audit: ...), got %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("fail-closed: expected zero rows, got %d (literal leak)", len(rows))
	}
}

// Happy path: exactly one data_tier=2 audit row is appended and the chain
// stays intact.
func TestT2Read_HappyPath_auditsExactlyOnceAndVerifies(t *testing.T) {
	f := newT2Fixture(t, true, true)
	ctx := context.Background()

	before := f.tier2AuditCount(t)
	rows, err := f.reader.ReadT2QueryStats(ctx, f.request("alice"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 T2 row, got %d", len(rows))
	}
	if rows[0].NormalizedQuery == "" || !strings.Contains(rows[0].NormalizedQuery, "ssn") {
		t.Fatalf("expected the T2 literal row back, got %+v", rows[0])
	}
	if got := f.tier2AuditCount(t) - before; got != 1 {
		t.Fatalf("data_tier=2 audit rows delta = %d, want 1", got)
	}

	idx, reason, err := store.NewConfig(f.cfg).VerifyChain(ctx, time.Time{}, time.Time{})
	if err != nil || idx != -1 || reason != "" {
		t.Fatalf("VerifyChain = (%d, %q, %v), want (-1, \"\", nil)", idx, reason, err)
	}
}

// Tier fast-reject: t2_enabled=false → rejected BEFORE any audit write.
func TestT2Read_TierFastReject_noAuditNoRows(t *testing.T) {
	f := newT2Fixture(t, false, true)
	ctx := context.Background()

	before := f.tier2AuditCount(t)
	rows, err := f.reader.ReadT2QueryStats(ctx, f.request("alice"))
	if err == nil {
		t.Fatal("expected error when t2_enabled=false")
	}
	if len(rows) != 0 {
		t.Fatalf("expected zero rows on tier reject, got %d", len(rows))
	}
	if got := f.tier2AuditCount(t) - before; got != 0 {
		t.Fatalf("data_tier=2 audit rows delta = %d, want 0 (no audit before fast reject)", got)
	}
}

// Authorization deny: t2_enabled=true but EffectiveCapability not enabled
// (no policy row) → denied, no literal, and (per the plan's decision) no
// data_tier=2 audit row.
func TestT2Read_AuthDeny_noLiteralNoAudit(t *testing.T) {
	f := newT2Fixture(t, true, false) // no capability policy → not authorized
	ctx := context.Background()

	before := f.tier2AuditCount(t)
	rows, err := f.reader.ReadT2QueryStats(ctx, f.request("alice"))
	if err == nil {
		t.Fatal("expected error when capability not authorized")
	}
	if len(rows) != 0 {
		t.Fatalf("expected zero rows on auth deny, got %d", len(rows))
	}
	if got := f.tier2AuditCount(t) - before; got != 0 {
		t.Fatalf("data_tier=2 audit rows delta = %d, want 0 (deny is not audited as a read)", got)
	}
}

// Source-of-truth: flipping the config-DB rows flips the gate. Start
// denied (t2_enabled=false), then set the servers row true and add an
// enabled capability policy; the same request now succeeds.
func TestT2Read_ConfigDBIsSourceOfTruth(t *testing.T) {
	f := newT2Fixture(t, false, false)
	ctx := context.Background()

	if _, err := f.reader.ReadT2QueryStats(ctx, f.request("alice")); err == nil {
		t.Fatal("expected deny while config-DB rows say disabled")
	}

	if _, err := f.cfg.Exec(ctx,
		`UPDATE servers SET t2_enabled = true WHERE id = $1`, f.serverID); err != nil {
		t.Fatalf("flip t2_enabled: %v", err)
	}
	if _, err := store.NewConfig(f.cfg).SetCapabilityPolicy(ctx, store.SetCapabilityPolicyInput{
		ServerID: f.serverID, Capability: f.capName, Enabled: true, SetBy: "admin", Reason: "grant",
	}); err != nil {
		t.Fatalf("enable capability: %v", err)
	}

	rows, err := f.reader.ReadT2QueryStats(ctx, f.request("alice"))
	if err != nil {
		t.Fatalf("read after config flip: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after config flip, got %d", len(rows))
	}
}

// No-unaudited-T2-path guard: exactly one `data_tier = 2` SELECT literal
// exists in non-test store source, and it lives in the tier-2 read method
// the gateway calls. Demonstrates the gateway is the single choke point.
func TestT2Read_OnlyOneTier2SelectInStoreSource(t *testing.T) {
	root := "." // internal/store
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	hits := 0
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		n := strings.Count(string(b), "data_tier = 2")
		if n > 0 {
			hits += n
			files = append(files, e.Name())
		}
	}
	if hits != 1 {
		t.Fatalf("`data_tier = 2` appears %d time(s) in %v; must be exactly 1 (the gateway choke point)", hits, files)
	}
	if len(files) != 1 || files[0] != "t2_read.go" {
		t.Fatalf("`data_tier = 2` must live only in t2_read.go, found in %v", files)
	}
}
