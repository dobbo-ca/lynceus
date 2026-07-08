// White-box unit tests for the per-process ensured-partition cache. They
// live in package store (not store_test) so they can seed s.ensured and
// construct a pgxStats with a nil pool: a cache hit must short-circuit
// before s.pool.Exec, so a nil return (no panic) proves the CREATE TABLE
// round-trip was skipped.
package store

import (
	"context"
	"testing"
	"time"
)

func TestEnsureWeeklyPartition_skipsRoundTripWhenCached(t *testing.T) {
	ts := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	s := &pgxStats{} // nil pool: any Exec would nil-deref and panic
	s.ensured.Store(partitionName(ts), struct{}{})

	if err := s.EnsureWeeklyPartition(context.Background(), ts); err != nil {
		t.Fatalf("cached EnsureWeeklyPartition = %v, want nil (round-trip skipped)", err)
	}
}

func TestEnsureActivityWeeklyPartition_skipsRoundTripWhenCached(t *testing.T) {
	ts := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	s := &pgxStats{} // nil pool: any Exec would nil-deref and panic
	s.ensured.Store(activityPartitionName(ts), struct{}{})

	if err := s.EnsureActivityWeeklyPartition(context.Background(), ts); err != nil {
		t.Fatalf("cached EnsureActivityWeeklyPartition = %v, want nil (round-trip skipped)", err)
	}
}
