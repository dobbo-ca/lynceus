package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

func TestWriteSettings_createsPartitionAndRoundtrips(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewStats(pool)

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) // a Tuesday
	rows := []store.SettingRow{
		{
			ServerID: "srv-a", CollectedAt: now,
			Name: "shared_buffers", Value: "16384", Unit: "8kB",
			Source: "configuration file", PendingRestart: false,
		},
		{
			ServerID: "srv-a", CollectedAt: now,
			Name: "fsync", Value: "off", Unit: "",
			Source: "override", PendingRestart: true,
		},
	}
	if err := s.WriteSettings(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.LatestSettings(ctx, "srv-a", now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(got), got)
	}

	byName := map[string]store.SettingRow{}
	for _, r := range got {
		byName[r.Name] = r
	}
	sb, ok := byName["shared_buffers"]
	if !ok || sb.Value != "16384" || sb.Unit != "8kB" || sb.Source != "configuration file" {
		t.Fatalf("shared_buffers row not preserved: %+v", sb)
	}
	if sb.DataTier != 1 {
		t.Errorf("expected data_tier coerced to 1, got %d", sb.DataTier)
	}
	fs, ok := byName["fsync"]
	if !ok || fs.Value != "off" || !fs.PendingRestart {
		t.Fatalf("fsync row not preserved: %+v", fs)
	}
}
