package checks

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
	"github.com/dobbo-ca/lynceus/internal/testpg"
)

// newSchedulerTestStore stands up a ClickHouse stats store (the sole stats
// backend) seeded with one table_stats row for "srv-a" so RecentServerIDs
// finds it, plus a vanilla Postgres pool for the scheduler's cross-replica
// advisory lock (ClickHouse has no advisory locks).
func newSchedulerTestStore(t *testing.T) (store.Stats, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewCHStats(conn)
	if err := s.WriteTableStats(ctx, []store.TableStatRow{{
		ServerID: "srv-a", CollectedAt: time.Now().UTC(),
		SchemaName: "public", ObjectName: "t", FQN: "public.t",
	}}); err != nil {
		t.Fatalf("seed table_stats: %v", err)
	}

	lockPool := testpg.Start(t)

	return s, lockPool
}

type recordingNotifier struct{ got []Result }

// Notify takes Result by value (per the Notifier contract) so it may retain
// its own copy; the by-value param is intentional.
//
//nolint:gocritic // hugeParam: Notifier.Notify is by-value by contract
func (n *recordingNotifier) Notify(_ context.Context, r Result) error {
	n.got = append(n.got, r)
	return nil
}

// alwaysCritical fires once for server input regardless of data.
type alwaysCritical struct{}

func (alwaysCritical) ID() string       { return "test.always" }
func (alwaysCritical) Category() string { return "test" }
func (alwaysCritical) Eval(in *Input) []Result {
	return []Result{{CheckID: "test.always", Category: "test",
		Severity: SeverityCritical, Status: StatusFiring, Object: "obj1", Detail: "x"}}
}

func TestSchedulerRunOncePersistsAndNotifies(t *testing.T) {
	ctx := context.Background()
	s, lockPool := newSchedulerTestStore(t)
	notif := &recordingNotifier{}
	sc := NewScheduler(s, lockPool, []Check{alwaysCritical{}}, notif).WithNow(func() time.Time {
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
	s, lockPool := newSchedulerTestStore(t)
	if err := s.SetMute(ctx, "srv-a", "test.always", "", time.Now().Add(time.Hour), "muted"); err != nil {
		t.Fatalf("mute: %v", err)
	}
	notif := &recordingNotifier{}
	sc := NewScheduler(s, lockPool, []Check{alwaysCritical{}}, notif)
	if err := sc.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(notif.got) != 0 {
		t.Fatalf("muted check must not notify, got %+v", notif.got)
	}
}
