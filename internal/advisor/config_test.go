package advisor

import "testing"

func findConfig(recs []ConfigRecommendation, setting string) *ConfigRecommendation {
	for i := range recs {
		if recs[i].Setting == setting {
			return &recs[i]
		}
	}
	return nil
}

func TestConfigAdvice_flagsUnsafeDurability(t *testing.T) {
	recs := ConfigAdvice([]ConfigSettingInput{
		{Name: "fsync", Value: "off", Source: "configuration file"},
		{Name: "full_page_writes", Value: "off", Source: "configuration file"},
		{Name: "synchronous_commit", Value: "off", Source: "configuration file"},
	})
	for _, want := range []struct {
		setting string
		sev     ConfigSeverity
	}{
		{"fsync", ConfHigh},
		{"full_page_writes", ConfHigh},
		{"synchronous_commit", ConfMedium},
	} {
		r := findConfig(recs, want.setting)
		if r == nil {
			t.Fatalf("%s not flagged", want.setting)
		}
		if r.Category != CatDurability {
			t.Errorf("%s category = %q, want durability", want.setting, r.Category)
		}
		if r.Severity != want.sev {
			t.Errorf("%s severity = %q, want %q", want.setting, r.Severity, want.sev)
		}
	}
}

func TestConfigAdvice_flagsDefaultMemory(t *testing.T) {
	recs := ConfigAdvice([]ConfigSettingInput{
		{Name: "shared_buffers", Value: "16384", Unit: "8kB", Source: "default"},
		{Name: "work_mem", Value: "4096", Unit: "kB", Source: "default"},
	})
	if r := findConfig(recs, "shared_buffers"); r == nil || r.Category != CatMemory || r.Severity != ConfMedium {
		t.Fatalf("shared_buffers = %+v, want memory/medium", r)
	}
	if r := findConfig(recs, "work_mem"); r == nil || r.Category != CatMemory || r.Severity != ConfLow {
		t.Fatalf("work_mem = %+v, want memory/low", r)
	}
}

func TestConfigAdvice_defaultMemory_nonDefaultSourceSilent(t *testing.T) {
	recs := ConfigAdvice([]ConfigSettingInput{
		{Name: "shared_buffers", Value: "16384", Unit: "8kB", Source: "configuration file"},
	})
	if r := findConfig(recs, "shared_buffers"); r != nil {
		t.Errorf("shared_buffers flagged despite non-default source: %+v", r)
	}
}

func TestConfigAdvice_flagsAutovacuumAndPlannerDefaults(t *testing.T) {
	recs := ConfigAdvice([]ConfigSettingInput{
		{Name: "autovacuum_vacuum_scale_factor", Value: "0.2", Source: "default"},
		{Name: "autovacuum_vacuum_cost_limit", Value: "200", Source: "default"},
		{Name: "random_page_cost", Value: "4", Source: "default"},
	})
	if r := findConfig(recs, "autovacuum_vacuum_scale_factor"); r == nil || r.Category != CatAutovacuum || r.Severity != ConfMedium {
		t.Fatalf("scale_factor = %+v, want autovacuum/medium", r)
	}
	if r := findConfig(recs, "autovacuum_vacuum_cost_limit"); r == nil || r.Category != CatAutovacuum || r.Severity != ConfLow {
		t.Fatalf("cost_limit = %+v, want autovacuum/low", r)
	}
	if r := findConfig(recs, "random_page_cost"); r == nil || r.Category != CatPlanner || r.Severity != ConfLow {
		t.Fatalf("random_page_cost = %+v, want planner/low", r)
	}
}

func TestConfigAdvice_sortedSeverityDesc(t *testing.T) {
	recs := ConfigAdvice([]ConfigSettingInput{
		{Name: "random_page_cost", Value: "4", Source: "default"}, // low/planner
		{Name: "fsync", Value: "off", Source: "configuration file"}, // high/durability
	})
	if len(recs) < 2 {
		t.Fatalf("want 2 recs, got %d", len(recs))
	}
	if recs[0].Setting != "fsync" {
		t.Errorf("first rec = %q, want fsync (severity desc)", recs[0].Setting)
	}
}

func TestConfigAdvice_tunedConfigSilent(t *testing.T) {
	recs := ConfigAdvice([]ConfigSettingInput{
		{Name: "fsync", Value: "on", Source: "default"},
		{Name: "full_page_writes", Value: "on", Source: "default"},
		{Name: "synchronous_commit", Value: "on", Source: "default"},
		{Name: "shared_buffers", Value: "1048576", Unit: "8kB", Source: "configuration file"},
		{Name: "work_mem", Value: "65536", Unit: "kB", Source: "user"},
		{Name: "maintenance_work_mem", Value: "524288", Unit: "kB", Source: "configuration file"},
		{Name: "autovacuum_vacuum_scale_factor", Value: "0.05", Source: "configuration file"},
		{Name: "autovacuum_vacuum_cost_limit", Value: "2000", Source: "configuration file"},
		{Name: "random_page_cost", Value: "1.1", Source: "configuration file"},
	})
	if len(recs) != 0 {
		t.Errorf("tuned config flagged: %+v", recs)
	}
}
