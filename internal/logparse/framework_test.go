// Integration test for the full log-parsing framework. Spins up real
// Postgres 16 with csvlog enabled, executes literal-bearing queries
// that produce known log lines, reads the produced log file back, runs
// it through ParseStream, and asserts:
//   - Every produced LogEvent has a recognised classification.
//   - No LogEvent string field contains any of the canary literals
//     that we deliberately seeded into the workload.
//   - The corresponding LogPayload carries those literals (so PII
//     filters downstream have something to redact).
package logparse_test

import (
	"bytes"
	_ "embed"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dobbo-ca/lynceus/internal/logparse"
)

func TestParseStream_realPostgresCsvlog(t *testing.T) {
	ctx := context.Background()

	// logging_collector=on redirects pg's "ready to accept connections"
	// banner into the log file, which defeats the default log-line wait
	// strategy. Wait on the listening port instead — it doesn't depend
	// on stderr content.
	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_logtest"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithCmd(
			"postgres",
			"-c", "logging_collector=on",
			"-c", "log_destination=csvlog",
			"-c", "log_directory=/var/lib/postgresql/data/log",
			"-c", "log_filename=postgres.log",
			"-c", "log_min_duration_statement=0",
			"-c", "log_connections=on",
			"-c", "log_disconnections=on",
		),
		testcontainers.WithWaitStrategyAndDeadline(
			120*time.Second,
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(120*time.Second),
		),
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

	const canaryEmail = "canary-leak@phi.example.com"
	const canaryTable = "patients_canary_table"

	for _, stmt := range []string{
		`CREATE TABLE ` + canaryTable + ` (id INT PRIMARY KEY, email TEXT)`,
		`INSERT INTO ` + canaryTable + ` VALUES (1, '` + canaryEmail + `')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	_, _ = pool.Exec(ctx, `INSERT INTO `+canaryTable+` VALUES (1, '`+canaryEmail+`')`)
	_, _ = pool.Exec(ctx, `SELCT 1`)

	time.Sleep(2 * time.Second)

	rc, err := c.CopyFileFromContainer(ctx, "/var/lib/postgresql/data/log/postgres.csv")
	if err != nil {
		rc, err = c.CopyFileFromContainer(ctx, "/var/lib/postgresql/data/log/postgres.log")
		if err != nil {
			t.Fatalf("copy log file: %v", err)
		}
	}
	defer rc.Close()

	tmp := filepath.Join(t.TempDir(), "postgres.csv")
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, rc); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	in, err := os.Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	events, payloads, err := logparse.ParseStream(in, logparse.Options{
		Format:   logparse.FormatCSV,
		LoggedAt: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events parsed from real postgres log")
	}
	if len(events) != len(payloads) {
		t.Fatalf("event/payload count mismatch: %d vs %d", len(events), len(payloads))
	}

	canaries := []string{canaryEmail, "canary-leak", "phi.example.com"}
	for i, ev := range events {
		for _, f := range []string{
			string(ev.EventType), ev.Severity.String(), ev.BackendType,
			ev.DatabaseName, ev.UserName, ev.AppName, ev.ClientAddrHash, ev.SQLState,
		} {
			for _, cn := range canaries {
				if strings.Contains(f, cn) {
					t.Fatalf("LITERAL LEAK in event %d field %q: contains %q", i, f, cn)
				}
			}
		}
	}

	wantSeen := map[logparse.EventType]bool{
		logparse.EventQueryDuration:            false,
		logparse.EventErrorConstraintViolation: false,
		logparse.EventErrorSyntax:              false,
	}
	for _, ev := range events {
		if _, want := wantSeen[ev.EventType]; want {
			wantSeen[ev.EventType] = true
		}
	}
	for et, seen := range wantSeen {
		if !seen {
			t.Errorf("expected to see at least one %s event in real-postgres log", et)
		}
	}

	sawSensitive := false
	for _, p := range payloads {
		if p.Tier() == logparse.TierSensitive {
			sawSensitive = true
			break
		}
	}
	if !sawSensitive {
		t.Error("no TierSensitive payload produced — sensitive payload must be preserved for PII filters")
	}
}

//go:embed testdata/csvlog_sample.csv
var csvFixture []byte

//go:embed testdata/stderr_sample.log
var stderrFixture []byte

func TestParseStream_offlineFixtures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []byte
		format logparse.Format
	}{
		{"csv", csvFixture, logparse.FormatCSV},
		{"stderr", stderrFixture, logparse.FormatStderr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, pl, err := logparse.ParseStream(bytes.NewReader(tc.data), logparse.Options{Format: tc.format})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(ev) == 0 {
				t.Fatal("fixture produced no events")
			}
			classified := 0
			for _, e := range ev {
				if e.EventType != logparse.EventUnclassified {
					classified++
				}
			}
			if classified == 0 {
				t.Error("fixture produced only unclassified events — rules regression?")
			}
			if len(ev) != len(pl) {
				t.Errorf("len(events)=%d len(payloads)=%d", len(ev), len(pl))
			}
		})
	}
}
