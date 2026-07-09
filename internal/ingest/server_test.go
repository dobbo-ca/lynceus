// Integration tests for the ingestion websocket server. They wire
// the real Server against a real Postgres (testcontainers) and a
// real Shipper, then assert two properties:
//
//   - the happy path actually lands rows in query_stats; and
//   - an over-limit second snapshot is parked in dlq (never lost).
package ingest_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/collector"
	"github.com/dobbo-ca/lynceus/internal/ingest"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

func setup(t *testing.T, cfg ingest.Config) (*pgxpool.Pool, *httptest.Server) {
	t.Helper()
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_stats"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testpg.ReadyWait(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	srv := httptest.NewServer(ingest.NewServer(cfg, store.NewStats(pool), pool).Handler())
	t.Cleanup(srv.Close)
	return pool, srv
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func makeSnapshot(serverID, fp, q string, totalMs float64) *lynceusv1.Snapshot {
	return &lynceusv1.Snapshot{
		ServerId:        serverID,
		CollectedAtUnix: time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC).Unix(),
		QueryStats: []*lynceusv1.QueryStat{{
			Fingerprint:     fp,
			NormalizedQuery: q,
			Calls:           7,
			TotalTimeMs:     totalMs,
		}},
	}
}

func TestServer_acceptsValidSnapshotAndPersistsToStatsDB(t *testing.T) {
	pool, srv := setup(t, ingest.Config{
		DevToken:  "dev",
		RateLimit: 10, RateBurst: 10,
	})
	ctx := context.Background()

	ship := collector.NewShipper(wsURL(srv.URL), "dev")
	if err := ship.Send(ctx, makeSnapshot("srv-1", "fp-A", "SELECT $1", 42)); err != nil {
		t.Fatalf("send: %v", err)
	}

	var rows int
	for i := 0; i < 50 && rows == 0; i++ {
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM query_stats WHERE server_id='srv-1' AND fingerprint='fp-A'`,
		).Scan(&rows)
		if rows > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rows != 1 {
		t.Fatalf("query_stats row count = %d, want 1", rows)
	}

	var dlq int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM dlq`).Scan(&dlq)
	if dlq != 0 {
		t.Errorf("dlq count = %d, want 0 (nothing should have been parked)", dlq)
	}
}

func TestServer_persistsSchemaObjectsWithServerSideFirstSeen(t *testing.T) {
	pool, srv := setup(t, ingest.Config{
		DevToken:  "dev",
		RateLimit: 10, RateBurst: 10,
	})
	ctx := context.Background()

	// The collector ships the inventory first-seen-less (FirstSeenAtUnix
	// left 0); the ingestion upsert must stamp first_seen_at server-side.
	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-inv",
		CollectedAtUnix: time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC).Unix(),
		SchemaObjects: []*lynceusv1.SchemaObject{{
			Kind:      lynceusv1.ObjectKind_OBJECT_KIND_TABLE,
			Schema:    "public",
			Name:      "orders",
			Fqn:       "public.orders",
			SizeBytes: 8192,
		}},
	}
	ship := collector.NewShipper(wsURL(srv.URL), "dev")
	if err := ship.Send(ctx, snap); err != nil {
		t.Fatalf("send: %v", err)
	}

	var rows int
	for i := 0; i < 50 && rows == 0; i++ {
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM schema_objects WHERE server_id='srv-inv' AND fqn='public.orders'`,
		).Scan(&rows)
		if rows > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rows != 1 {
		t.Fatalf("schema_objects row count = %d, want 1", rows)
	}

	var firstSeen time.Time
	if err := pool.QueryRow(ctx,
		`SELECT first_seen_at FROM schema_objects WHERE server_id='srv-inv' AND fqn='public.orders'`,
	).Scan(&firstSeen); err != nil {
		t.Fatalf("read first_seen_at: %v", err)
	}
	if firstSeen.IsZero() {
		t.Error("first_seen_at must be stamped server-side by the upsert, not carried from the collector")
	}
}

func TestIngest_writesTableStats(t *testing.T) {
	pool, srv := setup(t, ingest.Config{
		DevToken:  "dev",
		RateLimit: 10, RateBurst: 10,
	})
	ctx := context.Background()

	now := time.Now().UTC()
	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-ts",
		CollectedAtUnix: now.Unix(),
		TableStats: []*lynceusv1.TableStat{{
			Schema: "reporting", Name: "events", Fqn: "reporting.events",
			TotalBytes: 300, HeapBytes: 100, ToastBytes: 120, IndexesBytes: 80,
			RowEstimate: 1000, LiveTuples: 900, DeadTuples: 50,
			VacuumCount: 2, AutovacuumCount: 3,
		}},
	}
	ship := collector.NewShipper(wsURL(srv.URL), "dev")
	if err := ship.Send(ctx, snap); err != nil {
		t.Fatalf("send: %v", err)
	}

	stats := store.NewStats(pool)
	out, err := stats.LatestTableStats(ctx, "srv-ts", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(out) != 1 || out[0].FQN != "reporting.events" {
		t.Fatalf("table_stats row not persisted: %+v", out)
	}
	if out[0].ToastBytes != 120 || out[0].TotalBytes != 300 {
		t.Errorf("sizes not persisted: %+v", out[0])
	}
}

func TestIngest_writesIndexStats(t *testing.T) {
	pool, srv := setup(t, ingest.Config{
		DevToken:  "dev",
		RateLimit: 10, RateBurst: 10,
	})
	ctx := context.Background()

	now := time.Now().UTC()
	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-ix",
		CollectedAtUnix: now.Unix(),
		IndexStats: []*lynceusv1.IndexStat{{
			Schema: "public", Name: "t_pkey", Fqn: "public.t_pkey",
			TableFqn: "public.t", IdxScan: 5, SizeBytes: 8192,
			IsValid: true, IsReady: true, IsUnique: true, IsPrimary: true,
		}},
	}
	ship := collector.NewShipper(wsURL(srv.URL), "dev")
	if err := ship.Send(ctx, snap); err != nil {
		t.Fatalf("send: %v", err)
	}

	stats := store.NewStats(pool)
	out, err := stats.LatestIndexStats(ctx, "srv-ix", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(out) != 1 || out[0].FQN != "public.t_pkey" {
		t.Fatalf("index_stats row not persisted: %+v", out)
	}
	if !out[0].IsPrimary || !out[0].IsUnique || out[0].IdxScan != 5 {
		t.Errorf("index flags/scan not persisted: %+v", out[0])
	}
}

func TestIngest_writesSettings(t *testing.T) {
	pool, srv := setup(t, ingest.Config{
		DevToken:  "dev",
		RateLimit: 10, RateBurst: 10,
	})
	ctx := context.Background()

	now := time.Now().UTC()
	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-set",
		CollectedAtUnix: now.Unix(),
		Settings: []*lynceusv1.Setting{{
			Name: "shared_buffers", Value: "16384", Unit: "8kB",
			Source: "configuration file", PendingRestart: false,
		}, {
			Name: "fsync", Value: "off", Unit: "",
			Source: "override", PendingRestart: true,
		}},
	}
	ship := collector.NewShipper(wsURL(srv.URL), "dev")
	if err := ship.Send(ctx, snap); err != nil {
		t.Fatalf("send: %v", err)
	}

	stats := store.NewStats(pool)
	out, err := stats.LatestSettings(ctx, "srv-set", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	byName := map[string]store.SettingRow{}
	for _, r := range out {
		byName[r.Name] = r
	}
	if len(out) != 2 || byName["shared_buffers"].Value != "16384" || byName["fsync"].Value != "off" {
		t.Fatalf("settings rows not persisted: %+v", out)
	}
	if !byName["fsync"].PendingRestart {
		t.Errorf("pending_restart not persisted: %+v", byName["fsync"])
	}
}

//nolint:gocyclo // scenario-driven integration test; the assertions make complexity inherent
func TestIngest_persistsLogEvents_alongsideQueryPlans(t *testing.T) {
	pool, srv := setup(t, ingest.Config{
		DevToken: "dev", RateLimit: 10, RateBurst: 10,
	})
	ctx := context.Background()

	occurred := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC).Unix()
	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-logpark",
		CollectedAtUnix: time.Now().Unix(),
		LogEvents: []*lynceusv1.LogEvent{
			{EventType: "checkpoint.completed", Severity: "LOG", Pid: 7,
				OccurredAtUnix: occurred, LoggedAtUnix: occurred, BackendType: "checkpointer"},
			{EventType: "lock.deadlock_detected", Severity: "ERROR", Pid: 22,
				OccurredAtUnix: occurred, LoggedAtUnix: occurred,
				BackendType: "client backend", DatabaseName: "app", UserName: "alice",
				ApplicationName: "psql", ClientAddrHash: "abc123", SqlState: "40P01",
				SessionLineNum: 9, TransactionId: 77},
		},
		QueryPlans: []*lynceusv1.QueryPlan{
			{Fingerprint: "fp-logpark", CapturedAtUnix: time.Now().Unix(),
				Root: &lynceusv1.PlanNode{NodeType: "Seq Scan", RelationName: "orders"}},
		},
	}
	if err := collector.NewShipper(wsURL(srv.URL), "dev").Send(ctx, snap); err != nil {
		t.Fatalf("shipper send (log events should be accepted, not rejected): %v", err)
	}

	var plans int
	for i := 0; i < 100 && plans == 0; i++ {
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM query_plans WHERE server_id = 'srv-logpark'`).Scan(&plans)
		if plans > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if plans == 0 {
		t.Fatal("query_plans did not persist — the log-event path must not break the plan path")
	}

	// Shipped LogEvents now land in the log_events table.
	var events int
	for i := 0; i < 100 && events == 0; i++ {
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM log_events WHERE server_id = 'srv-logpark'`).Scan(&events)
		if events > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if events != 2 {
		t.Fatalf("log_events row count = %d, want 2", events)
	}

	// Classification fields round-trip on the deadlock event.
	var (
		sev, db, usr, app, hash, state string
		pid, line, txid                int64
		tier                           int16
	)
	if err := pool.QueryRow(ctx,
		`SELECT severity, database_name, user_name, application_name,
		        client_addr_hash, sql_state, pid, session_line_num, transaction_id, data_tier
		   FROM log_events
		  WHERE server_id = 'srv-logpark' AND event_type = 'lock.deadlock_detected'`,
	).Scan(&sev, &db, &usr, &app, &hash, &state, &pid, &line, &txid, &tier); err != nil {
		t.Fatalf("read log_event: %v", err)
	}
	if sev != "ERROR" || db != "app" || usr != "alice" || app != "psql" ||
		hash != "abc123" || state != "40P01" || pid != 22 || line != 9 || txid != 77 || tier != 1 {
		t.Errorf("log_event fields not persisted: sev=%q db=%q usr=%q app=%q hash=%q state=%q pid=%d line=%d txid=%d tier=%d",
			sev, db, usr, app, hash, state, pid, line, txid, tier)
	}

	// A successful persist parks nothing.
	var dlq int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM dlq WHERE server_id = 'srv-logpark'`).Scan(&dlq)
	if dlq != 0 {
		t.Fatalf("nothing should be parked on the happy path; dlq rows = %d", dlq)
	}
}

func TestServer_parksOverLimitSnapshotInDLQ(t *testing.T) {
	// Per-server rate.Limit of 1/s with burst 1: the first snapshot
	// consumes the burst, the second arrives "too soon" and must be
	// DLQ'd rather than dropped.
	pool, srv := setup(t, ingest.Config{
		DevToken:  "dev",
		RateLimit: 1, RateBurst: 1,
	})
	ctx := context.Background()
	ship := collector.NewShipper(wsURL(srv.URL), "dev")

	if err := ship.Send(ctx, makeSnapshot("srv-2", "fp-1st", "SELECT $1", 1)); err != nil {
		t.Fatalf("first send: %v", err)
	}
	// Second send immediately — should be rate-limited and parked.
	err := ship.Send(ctx, makeSnapshot("srv-2", "fp-2nd", "SELECT $1", 2))
	// The server closes the ws with StatusTryAgainLater; our Shipper
	// may report close-handshake difficulty but the DLQ insert is
	// what we actually care about.
	_ = err

	// Wait for DLQ insertion to land (it may race with the close).
	var dlq int
	for i := 0; i < 50 && dlq == 0; i++ {
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM dlq WHERE server_id='srv-2' AND reason='rate_limited'`,
		).Scan(&dlq)
		if dlq > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if dlq != 1 {
		t.Errorf("dlq count for srv-2 = %d, want 1 (over-limit must be parked, not dropped)", dlq)
	}

	// First snapshot should still have landed in query_stats.
	var qs int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM query_stats WHERE server_id='srv-2'`,
	).Scan(&qs)
	if qs != 1 {
		t.Errorf("query_stats for srv-2 = %d, want 1", qs)
	}
}

// TestServer_derivesAndPersistsInsightsFromPlan sends a Snapshot carrying a
// slow-scan QueryPlan and asserts the ingest server derives + persists an
// insights row (server-side derivation; no collector emission).
func TestServer_derivesAndPersistsInsightsFromPlan(t *testing.T) {
	pool, srv := setup(t, ingest.Config{DevToken: "dev", RateLimit: 10, RateBurst: 10})
	ctx := context.Background()

	captured := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-ins",
		CollectedAtUnix: captured.Unix(),
		QueryPlans: []*lynceusv1.QueryPlan{{
			Fingerprint:    "fp-slow",
			CapturedAtUnix: captured.Unix(),
			FormatVersion:  1,
			Root: &lynceusv1.PlanNode{
				NodeType:            "Seq Scan",
				RelationName:        "events",
				ActualRows:          10,
				ActualLoops:         1,
				RowsRemovedByFilter: 99990,
			},
		}},
	}

	ship := collector.NewShipper(wsURL(srv.URL), "dev")
	if err := ship.Send(ctx, snap); err != nil {
		t.Fatalf("send: %v", err)
	}

	var rows int
	for i := 0; i < 50 && rows == 0; i++ {
		_ = pool.QueryRow(ctx,
			`SELECT count(*) FROM insights WHERE server_id='srv-ins' AND kind='slow_scan'`,
		).Scan(&rows)
		if rows > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rows != 1 {
		t.Fatalf("insights row count = %d, want 1", rows)
	}

	var sev, rel string
	if err := pool.QueryRow(ctx,
		`SELECT severity, relation FROM insights WHERE server_id='srv-ins'`,
	).Scan(&sev, &rel); err != nil {
		t.Fatalf("read insight: %v", err)
	}
	if sev != "high" || rel != "events" {
		t.Fatalf("insight = (%s, %s), want (high, events)", sev, rel)
	}

	var dlq int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM dlq`).Scan(&dlq)
	if dlq != 0 {
		t.Errorf("dlq count = %d, want 0", dlq)
	}
}
