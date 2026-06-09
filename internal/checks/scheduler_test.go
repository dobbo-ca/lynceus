package checks

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// newSchedulerTestStore spins up a fresh postgres:16 via testcontainers,
// applies the stats migrations, and seeds one table_stats row for "srv-a"
// so RecentServerIDs finds it. Mirrors internal/store's newPool helper
// (duplicated minimally here since that helper lives in package store_test
// and is not importable from this package).
func newSchedulerTestStore(t *testing.T) *store.Stats {
	t.Helper()
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("lynceus_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)
	if err := s.WriteTableStats(ctx, []store.TableStatRow{{
		ServerID: "srv-a", CollectedAt: time.Now().UTC(),
		SchemaName: "public", ObjectName: "t", FQN: "public.t",
	}}); err != nil {
		t.Fatalf("seed table_stats: %v", err)
	}
	return s
}

type recordingNotifier struct{ got []Result }

func (n *recordingNotifier) Notify(_ context.Context, r Result) error {
	n.got = append(n.got, r)
	return nil
}

// alwaysCritical fires once for server input regardless of data.
type alwaysCritical struct{}

func (alwaysCritical) ID() string      { return "test.always" }
func (alwaysCritical) Category() string { return "test" }
func (alwaysCritical) Eval(in Input) []Result {
	return []Result{{CheckID: "test.always", Category: "test",
		Severity: SeverityCritical, Status: StatusFiring, Object: "obj1", Detail: "x"}}
}

func TestSchedulerRunOncePersistsAndNotifies(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerTestStore(t)
	notif := &recordingNotifier{}
	sc := NewScheduler(s, []Check{alwaysCritical{}}, notif).WithNow(func() time.Time {
		return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	})
	if err := sc.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	res, err := s.LatestChecksResults(ctx, "srv-a",
		time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC))
	if err != nil || len(res) != 1 {
		t.Fatalf("persist: err=%v rows=%d", err, len(res))
	}
	if len(notif.got) != 1 || notif.got[0].CheckID != "test.always" {
		t.Fatalf("notify: %+v", notif.got)
	}
}

func TestSchedulerHonorsMute(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerTestStore(t)
	if err := s.SetMute(ctx, "srv-a", "test.always", "", time.Now().Add(time.Hour), "muted"); err != nil {
		t.Fatalf("mute: %v", err)
	}
	notif := &recordingNotifier{}
	sc := NewScheduler(s, []Check{alwaysCritical{}}, notif)
	if err := sc.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(notif.got) != 0 {
		t.Fatalf("muted check must not notify, got %+v", notif.got)
	}
}
