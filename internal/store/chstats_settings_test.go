package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/internal/testch"
)

func settingsNewCH(t *testing.T) (context.Context, store.Stats) {
	t.Helper()
	ctx := context.Background()
	conn := testch.Start(t)
	if err := store.ApplyClickHouseMigrations(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, store.NewCHStats(conn)
}

func TestCH_settings_WriteAndLatest_RoundTrip(t *testing.T) {
	ctx, s := settingsNewCH(t)

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

	// ORDER BY name: fsync before shared_buffers.
	if got[0].Name != "fsync" || got[1].Name != "shared_buffers" {
		t.Fatalf("unexpected order: %s, %s", got[0].Name, got[1].Name)
	}

	byName := map[string]store.SettingRow{}
	for _, r := range got {
		byName[r.Name] = r
	}
	sb := byName["shared_buffers"]
	if sb.Value != "16384" || sb.Unit != "8kB" || sb.Source != "configuration file" {
		t.Fatalf("shared_buffers row not preserved: %+v", sb)
	}
	if sb.DataTier != 1 {
		t.Errorf("expected data_tier coerced to 1, got %d", sb.DataTier)
	}
	if sb.PendingRestart {
		t.Errorf("shared_buffers pending_restart should be false: %+v", sb)
	}
	if !sb.CollectedAt.Equal(now) {
		t.Errorf("collected_at not preserved: got %v want %v", sb.CollectedAt, now)
	}
	fsync := byName["fsync"]
	if fsync.Value != "off" || !fsync.PendingRestart {
		t.Fatalf("fsync row not preserved: %+v", fsync)
	}
}

func TestCH_settings_Latest_PerNameAsOf(t *testing.T) {
	ctx, s := settingsNewCH(t)

	base := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	later := base.Add(time.Hour)
	future := base.Add(2 * time.Hour)

	rows := []store.SettingRow{
		// two snapshots of the same GUC — latest-as-of must win.
		{ServerID: "srv-a", CollectedAt: base, Name: "work_mem", Value: "4096", Unit: "kB", Source: "default"},
		{ServerID: "srv-a", CollectedAt: later, Name: "work_mem", Value: "8192", Unit: "kB", Source: "user"},
		// a different server must not bleed in.
		{ServerID: "srv-b", CollectedAt: later, Name: "work_mem", Value: "1", Unit: "kB", Source: "default"},
	}
	if err := s.WriteSettings(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	// As of base: only the base snapshot is visible (value 4096).
	atBase, err := s.LatestSettings(ctx, "srv-a", base)
	if err != nil {
		t.Fatalf("read base: %v", err)
	}
	if len(atBase) != 1 || atBase[0].Value != "4096" {
		t.Fatalf("as-of base wrong: %+v", atBase)
	}

	// As of future: the later snapshot wins (value 8192), one row per name.
	atFuture, err := s.LatestSettings(ctx, "srv-a", future)
	if err != nil {
		t.Fatalf("read future: %v", err)
	}
	if len(atFuture) != 1 {
		t.Fatalf("want 1 row per name, got %d: %+v", len(atFuture), atFuture)
	}
	if atFuture[0].Value != "8192" || atFuture[0].Source != "user" {
		t.Fatalf("latest-as-of did not win: %+v", atFuture[0])
	}
	if !atFuture[0].CollectedAt.Equal(later) {
		t.Errorf("collected_at should be the latest: got %v want %v", atFuture[0].CollectedAt, later)
	}
}

func TestCH_settings_WriteEmpty_NoOp(t *testing.T) {
	ctx, s := settingsNewCH(t)
	if err := s.WriteSettings(ctx, nil); err != nil {
		t.Fatalf("empty write: %v", err)
	}
	got, err := s.LatestSettings(ctx, "srv-a", time.Now())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no rows, got %d", len(got))
	}
}
