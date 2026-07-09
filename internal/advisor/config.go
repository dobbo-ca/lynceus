package advisor

import (
	"sort"
	"strconv"
)

// ConfigCategory groups a tuning recommendation by the subsystem it touches.
type ConfigCategory string

const (
	CatMemory     ConfigCategory = "memory"
	CatDurability ConfigCategory = "durability"
	CatAutovacuum ConfigCategory = "autovacuum"
	CatPlanner    ConfigCategory = "planner"
)

// ConfigSeverity ranks a recommendation. It is a distinct type from
// VacuumSeverity even though the string values coincide.
type ConfigSeverity string

const (
	ConfLow    ConfigSeverity = "low"
	ConfMedium ConfigSeverity = "medium"
	ConfHigh   ConfigSeverity = "high"
)

// ConfigSettingInput is the advisor-local projection of store.SettingRow the
// api handler feeds in (name + bounded-config value + unit + source). Keeping
// it decoupled from store keeps ConfigAdvice pure and testable, mirroring
// TableVacuumInfo.
type ConfigSettingInput struct {
	Name   string
	Value  string
	Unit   string
	Source string
}

// ConfigRecommendation is one categorized tuning suggestion (T1-safe:
// identifiers + bounded-config values + package-authored guidance only, no
// monitored-DB literal). Suggested is a guidance CLASS, not a computed literal.
type ConfigRecommendation struct {
	Setting   string
	Category  ConfigCategory
	Severity  ConfigSeverity
	Current   string
	Suggested string
	Detail    string
}

// ConfigAdvice derives tuning recommendations from a curated pg_settings
// snapshot. It is SETTINGS-DERIVABLE ONLY and uses two signals: (a) absolute
// unsafe values (fsync/full_page_writes/synchronous_commit off) and
// (b) Source=="default" as the "untuned" flag for memory/autovacuum/planner
// suggestions — this avoids scolding intentionally small instances and is
// honest given we have no host-RAM signal here. Genuinely workload-correlated
// advice (shared_buffers vs cache-hit ratio, work_mem vs temp-file spills,
// autovacuum scale factor vs per-table dead-tuple growth) is a documented
// deferral (ly-u4t.18 follow-up): those store reads exist but wiring them would
// make this fn impure/heavier. Output is stably sorted severity-desc then
// Setting.
func ConfigAdvice(settings []ConfigSettingInput) []ConfigRecommendation {
	by := make(map[string]ConfigSettingInput, len(settings))
	for _, s := range settings {
		by[s.Name] = s
	}
	var out []ConfigRecommendation
	add := func(rec ConfigRecommendation) { out = append(out, rec) }

	// Durability — absolute unsafe values (no default-source gate: off is
	// dangerous however it was set).
	if s, ok := by["fsync"]; ok && s.Value == "off" {
		add(ConfigRecommendation{
			Setting: "fsync", Category: CatDurability, Severity: ConfHigh,
			Current: "off", Suggested: "on",
			Detail: "fsync is off — a crash can corrupt the cluster with unrecoverable data loss; re-enable.",
		})
	}
	if s, ok := by["full_page_writes"]; ok && s.Value == "off" {
		add(ConfigRecommendation{
			Setting: "full_page_writes", Category: CatDurability, Severity: ConfHigh,
			Current: "off", Suggested: "on",
			Detail: "full_page_writes is off — a crash mid-write can leave torn pages; re-enable.",
		})
	}
	if s, ok := by["synchronous_commit"]; ok && s.Value == "off" {
		add(ConfigRecommendation{
			Setting: "synchronous_commit", Category: CatDurability, Severity: ConfMedium,
			Current: "off", Suggested: "on",
			Detail: "synchronous_commit is off — recently committed transactions can be lost within the async window.",
		})
	}

	// Memory — Source=="default" is the untuned flag.
	if s, ok := by["shared_buffers"]; ok && s.Source == "default" {
		add(ConfigRecommendation{
			Setting: "shared_buffers", Category: CatMemory, Severity: ConfMedium,
			Current: s.Value, Suggested: "~25% of dedicated-host RAM",
			Detail: "shared_buffers is at its default; raise toward ~25% of dedicated-host memory.",
		})
	}
	if s, ok := by["work_mem"]; ok && s.Source == "default" {
		add(ConfigRecommendation{
			Setting: "work_mem", Category: CatMemory, Severity: ConfLow,
			Current: s.Value, Suggested: "raise per-operation sort/hash memory",
			Detail: "work_mem is at its default; large sorts and hash joins will spill to disk.",
		})
	}
	if s, ok := by["maintenance_work_mem"]; ok && s.Source == "default" {
		add(ConfigRecommendation{
			Setting: "maintenance_work_mem", Category: CatMemory, Severity: ConfLow,
			Current: s.Value, Suggested: "raise for faster VACUUM / CREATE INDEX",
			Detail: "maintenance_work_mem is at its default; maintenance operations are memory-starved.",
		})
	}

	// Autovacuum.
	if s, ok := by["autovacuum_vacuum_scale_factor"]; ok {
		if f, ok2 := parseFloat(s.Value); ok2 && f >= 0.2 {
			add(ConfigRecommendation{
				Setting: "autovacuum_vacuum_scale_factor", Category: CatAutovacuum, Severity: ConfMedium,
				Current: s.Value, Suggested: "lower (e.g. 0.05) so autovacuum triggers sooner",
				Detail: "autovacuum_vacuum_scale_factor is at or above the 0.2 default; large tables accumulate bloat before autovacuum fires.",
			})
		}
	}
	if s, ok := by["autovacuum_vacuum_cost_limit"]; ok && s.Source == "default" {
		add(ConfigRecommendation{
			Setting: "autovacuum_vacuum_cost_limit", Category: CatAutovacuum, Severity: ConfLow,
			Current: s.Value, Suggested: "raise so autovacuum keeps up under write load",
			Detail: "autovacuum_vacuum_cost_limit is at its default; autovacuum may fall behind on busy servers.",
		})
	}

	// Planner.
	if s, ok := by["random_page_cost"]; ok {
		if f, ok2 := parseFloat(s.Value); ok2 && f >= 4.0 {
			add(ConfigRecommendation{
				Setting: "random_page_cost", Category: CatPlanner, Severity: ConfLow,
				Current: s.Value, Suggested: "lower toward 1.1 on SSD/managed storage",
				Detail: "random_page_cost is at or above the 4.0 default; the planner over-penalizes index scans on SSD-backed storage.",
			})
		}
	}

	rank := map[ConfigSeverity]int{ConfHigh: 0, ConfMedium: 1, ConfLow: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		return out[i].Setting < out[j].Setting
	})
	return out
}

func parseFloat(v string) (float64, bool) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
