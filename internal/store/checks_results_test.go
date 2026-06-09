package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestWriteAndReadChecksResults(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	at := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	rows := []store.ChecksResultRow{{
		ServerID: "srv-a", EvaluatedAt: at, CheckID: "vacuum.wraparound",
		Category: "vacuum", Severity: "critical", Status: "firing",
		Object: "public.orders", Detail: "xid age 1.6e9 of 2e9",
	}}
	if err := s.WriteChecksResults(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := s.LatestChecksResults(ctx, "srv-a", at.Add(-time.Hour), at.Add(time.Hour))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].CheckID != "vacuum.wraparound" || got[0].Severity != "critical" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestMuteRoundTrip(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	until := time.Now().Add(time.Hour)
	if err := s.SetMute(ctx, "srv-a", "vacuum.wraparound", "", until, "planned maintenance"); err != nil {
		t.Fatalf("set: %v", err)
	}
	muted, err := s.ListMutes(ctx, "srv-a")
	if err != nil || len(muted) != 1 {
		t.Fatalf("list: %v %+v", err, muted)
	}
}
