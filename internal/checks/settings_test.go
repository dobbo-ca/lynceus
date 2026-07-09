package checks

import "testing"

// healthySettings is a well-tuned Input.Settings that every settings check
// must leave silent.
func healthySettings() []SettingInfo {
	return []SettingInfo{
		{Name: "autovacuum", Value: "on"},
		{Name: "track_counts", Value: "on"},
		{Name: "track_activities", Value: "on"},
		{Name: "fsync", Value: "on"},
		{Name: "full_page_writes", Value: "on"},
		{Name: "synchronous_commit", Value: "on"},
		{Name: "shared_buffers", Value: "1048576", Unit: "8kB"}, // 8 GiB
		{Name: "work_mem", Value: "65536", Unit: "kB"},          // 64 MiB
		{Name: "default_statistics_target", Value: "200"},       // above 100
		{Name: "track_io_timing", Value: "on"},
	}
}

func objects(rs []Result) map[string]Result {
	m := make(map[string]Result, len(rs))
	for _, r := range rs {
		m[r.Object] = r
	}
	return m
}

func TestSettingsDisabledFeatures_flagsAutovacuumAndTrackCounts(t *testing.T) {
	in := Input{Settings: []SettingInfo{
		{Name: "autovacuum", Value: "off"},
		{Name: "track_counts", Value: "off"},
		{Name: "track_activities", Value: "on"}, // healthy — must not fire
	}}
	got := DisabledFeaturesCheck{}.Eval(&in)
	m := objects(got)
	if len(got) != 2 {
		t.Fatalf("want 2 results (autovacuum, track_counts), got %+v", got)
	}
	if r, ok := m["autovacuum"]; !ok || r.Severity != SeverityCritical || r.CheckID != "settings.disabled_features" {
		t.Fatalf("want critical autovacuum, got %+v", m)
	}
	if r, ok := m["track_counts"]; !ok || r.Severity != SeverityCritical {
		t.Fatalf("want critical track_counts, got %+v", m)
	}
}

func TestSettingsFsync_flagsUnsafeDurability(t *testing.T) {
	in := Input{Settings: []SettingInfo{
		{Name: "fsync", Value: "off"},
		{Name: "full_page_writes", Value: "off"},
		{Name: "synchronous_commit", Value: "off"},
	}}
	got := FsyncCheck{}.Eval(&in)
	m := objects(got)
	if len(got) != 3 {
		t.Fatalf("want 3 durability results, got %+v", got)
	}
	if m["fsync"].Severity != SeverityCritical || m["full_page_writes"].Severity != SeverityCritical {
		t.Fatalf("want critical fsync + full_page_writes, got %+v", m)
	}
	if m["synchronous_commit"].Severity != SeverityWarning {
		t.Fatalf("want warning synchronous_commit, got %+v", m)
	}
}

// synchronous_commit=local is still locally durable and must not fire.
func TestSettingsFsync_localSyncCommitSilent(t *testing.T) {
	in := Input{Settings: []SettingInfo{
		{Name: "fsync", Value: "on"},
		{Name: "full_page_writes", Value: "on"},
		{Name: "synchronous_commit", Value: "local"},
	}}
	if got := (FsyncCheck{}).Eval(&in); len(got) != 0 {
		t.Fatalf("want silent for locally-durable config, got %+v", got)
	}
}

func TestSettingsSharedBuffers_flagsUndersized(t *testing.T) {
	// 16384 * 8kB = 128 MiB — exactly the default; <= threshold fires.
	in := Input{Settings: []SettingInfo{
		{Name: "shared_buffers", Value: "16384", Unit: "8kB"},
	}}
	got := SharedBuffersCheck{}.Eval(&in)
	if len(got) != 1 || got[0].Severity != SeverityWarning || got[0].Object != "shared_buffers" {
		t.Fatalf("want 1 warning shared_buffers, got %+v", got)
	}

	// Well-sized — silent.
	big := Input{Settings: []SettingInfo{{Name: "shared_buffers", Value: "1048576", Unit: "8kB"}}}
	if r := (SharedBuffersCheck{}).Eval(&big); len(r) != 0 {
		t.Fatalf("want silent for 8 GiB shared_buffers, got %+v", r)
	}
}

func TestSettingsStats_flagsLowTargetAndIoTiming(t *testing.T) {
	in := Input{Settings: []SettingInfo{
		{Name: "default_statistics_target", Value: "50"},
		{Name: "track_io_timing", Value: "off"},
	}}
	got := StatsCheck{}.Eval(&in)
	m := objects(got)
	if len(got) != 2 {
		t.Fatalf("want 2 stats results, got %+v", got)
	}
	if m["default_statistics_target"].Severity != SeverityInfo || m["track_io_timing"].Severity != SeverityInfo {
		t.Fatalf("want info default_statistics_target + track_io_timing, got %+v", m)
	}
	// default_statistics_target == 100 is not below threshold — silent.
	ok := Input{Settings: []SettingInfo{
		{Name: "default_statistics_target", Value: "100"},
		{Name: "track_io_timing", Value: "on"},
	}}
	if r := (StatsCheck{}).Eval(&ok); len(r) != 0 {
		t.Fatalf("want silent for target=100 + io_timing on, got %+v", r)
	}
}

func TestSettingsWorkMem_flagsDefault(t *testing.T) {
	// 4096 * kB = 4 MiB — the default; <= threshold fires.
	in := Input{Settings: []SettingInfo{{Name: "work_mem", Value: "4096", Unit: "kB"}}}
	got := WorkMemCheck{}.Eval(&in)
	if len(got) != 1 || got[0].Severity != SeverityInfo || got[0].Object != "work_mem" {
		t.Fatalf("want 1 info work_mem, got %+v", got)
	}
	big := Input{Settings: []SettingInfo{{Name: "work_mem", Value: "65536", Unit: "kB"}}}
	if r := (WorkMemCheck{}).Eval(&big); len(r) != 0 {
		t.Fatalf("want silent for 64 MiB work_mem, got %+v", r)
	}
}

func TestSettings_healthyConfigSilent(t *testing.T) {
	in := Input{Settings: healthySettings()}
	settingsChecks := []Check{
		DisabledFeaturesCheck{}, FsyncCheck{}, SharedBuffersCheck{}, StatsCheck{}, WorkMemCheck{},
	}
	for _, c := range settingsChecks {
		if got := c.Eval(&in); len(got) != 0 {
			t.Fatalf("%s fired on healthy config: %+v", c.ID(), got)
		}
	}
}
