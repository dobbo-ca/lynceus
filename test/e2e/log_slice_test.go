// End-to-end test for the ly-cxe.2 log-source pipeline.
//
// A real postgres:16 logs an auto_explain plan whose Filter references the
// canary literal. The collector tails the log file, drains it, and ships the
// resulting Snapshot to a real ingestion server. The canary MUST NOT appear in
// the persisted plan JSON nor in any shipped LogEvent's proto bytes.
//
// If this test ever fails, the privacy guarantee is broken — do not merge.
package e2e_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/protobuf/proto"

	"github.com/dobbo-ca/lynceus/internal/collector"
	"github.com/dobbo-ca/lynceus/internal/ingest"
	"github.com/dobbo-ca/lynceus/internal/logparse"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

func TestLogSlice_planExtractedAndCanaryNeverLeaks(t *testing.T) {
	ctx := context.Background()

	// --- target Postgres with auto_explain logging to stderr/json ---
	targetC, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("target"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithCmd("postgres",
			"-c", "shared_preload_libraries=auto_explain",
			"-c", "logging_collector=on",
			"-c", "log_directory=log",
			"-c", "log_filename=postgresql.log",
			"-c", "log_min_duration_statement=0",
			"-c", "auto_explain.log_min_duration=0",
			"-c", "auto_explain.log_format=json",
			"-c", "auto_explain.log_analyze=on",
		),
		// logging_collector=on redirects stderr into a log file, so a log-line
		// wait is unusable. testpg.ReadyWait() uses pg_isready over TCP, which
		// confirms the server actually accepts connections — a bare port wait
		// races the post-listen startup and yields "connection reset by peer".
		testpg.ReadyWait(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(targetC) })

	targetURL, err := targetC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	target, err := pgxpool.New(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(target.Close)

	for _, stmt := range []string{
		`CREATE TABLE patients (id INT PRIMARY KEY, email TEXT)`,
		`INSERT INTO patients SELECT g, 'a' || g || '@example.com' FROM generate_series(1, 2000) g`,
	} {
		if _, err := target.Exec(ctx, stmt); err != nil {
			t.Fatalf("target setup %q: %v", stmt, err)
		}
	}
	// A query whose plan body will reference the canary literal in its Filter.
	if _, err := target.Exec(ctx,
		`SELECT id FROM patients WHERE email = '`+canaryLiteral+`@example.com'`,
	); err != nil {
		t.Fatalf("target canary query: %v", err)
	}

	// --- copy the log file out of the container to a host path FileTail reads ---
	logPath := filepath.Join(t.TempDir(), "postgresql.log")
	deadline := time.Now().Add(30 * time.Second)
	for {
		rc, err := targetC.CopyFileFromContainer(ctx, "/var/lib/postgresql/data/log/postgresql.log")
		if err == nil {
			f, cerr := os.Create(logPath)
			if cerr != nil {
				t.Fatal(cerr)
			}
			_, _ = f.ReadFrom(rc)
			_ = f.Close()
			_ = rc.Close()
			if data, _ := os.ReadFile(logPath); bytes.Contains(data, []byte("plan:")) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Skipf("auto_explain plan line never appeared in the container log")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// --- stats Postgres + migrations + in-process ingestion server ---
	statsC, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("stats"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testpg.ReadyWait(),
	)
	if err != nil {
		t.Skipf("stats container unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(statsC) })

	statsURL, err := statsC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	statsPool, err := pgxpool.New(ctx, statsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(statsPool.Close)
	if err := store.ApplyStatsMigrations(ctx, statsPool); err != nil {
		t.Fatalf("apply stats migrations: %v", err)
	}

	ingSrv := httptest.NewServer(ingest.NewServer(
		ingest.Config{DevToken: "dev", RateLimit: 10, RateBurst: 10},
		store.NewStats(statsPool), statsPool,
	).Handler())
	t.Cleanup(ingSrv.Close)
	wsURL := "ws" + strings.TrimPrefix(ingSrv.URL, "http")

	// --- drive ONE Drain over the tailed file ---
	tail := collector.NewFileTail(logPath)
	defer tail.Close()
	pipe := collector.NewLogPipeline(tail, "srv-log-e2e",
		logparse.Options{Format: logparse.FormatStderr}, false)

	res, err := pipe.Drain()
	if err != nil {
		t.Fatalf("drain: %v", err)
	}

	// READER checkpoint: the canary must not survive into any shipped LogEvent
	// proto, and not into any extracted plan's normalized condition.
	for _, ev := range res.LogEvents {
		b, _ := proto.Marshal(ev)
		if bytes.Contains(b, []byte(canaryLiteral)) {
			t.Fatal("READER LEAKED CANARY: raw log payload reached a T1 LogEvent")
		}
	}
	if len(res.QueryPlans) == 0 {
		t.Fatal("no QueryPlan extracted from the auto_explain log — pipeline broken")
	}
	sawSeqScan := false
	for _, qp := range res.QueryPlans {
		pb, _ := proto.Marshal(qp)
		if bytes.Contains(pb, []byte(canaryLiteral)) {
			t.Fatalf("READER LEAKED CANARY in QueryPlan proto bytes")
		}
		if qp.GetFingerprint() == "" {
			t.Error("extracted plan has empty fingerprint")
		}
		if qp.GetRoot().GetNodeType() == "Seq Scan" {
			sawSeqScan = true
		}
	}
	if !sawSeqScan {
		t.Error("did not see the patients Seq Scan in the extracted plan")
	}

	// --- ship the snapshot to ingestion ---
	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-log-e2e",
		CollectedAtUnix: time.Now().Unix(),
		LogEvents:       res.LogEvents,
		QueryPlans:      res.QueryPlans,
	}
	if err := collector.NewShipper(wsURL, "dev").Send(ctx, snap); err != nil {
		t.Fatalf("shipper: %v", err)
	}

	// --- wait for the plan to persist ---
	var persisted int
	for i := 0; i < 100 && persisted == 0; i++ {
		_ = statsPool.QueryRow(ctx,
			`SELECT count(*) FROM query_plans WHERE server_id = 'srv-log-e2e'`,
		).Scan(&persisted)
		if persisted > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if persisted == 0 {
		t.Fatal("nothing persisted to query_plans — log pipeline broken")
	}

	// STORAGE checkpoint: the persisted plan JSON must not contain the canary.
	rows, err := statsPool.Query(ctx,
		`SELECT plan_tree::text FROM query_plans WHERE server_id = 'srv-log-e2e'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	sawPatients := false
	for rows.Next() {
		var planJSON string
		if err := rows.Scan(&planJSON); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(planJSON, canaryLiteral) {
			t.Fatalf("STORAGE LEAKED CANARY in persisted plan_tree: %q", planJSON)
		}
		if strings.Contains(planJSON, "patients") {
			sawPatients = true
		}
	}
	if !sawPatients {
		t.Error("did not find the patients relation in persisted plan — pipeline incomplete")
	}
}
