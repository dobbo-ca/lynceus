package checks

import (
	"fmt"
	"strconv"
	"strings"
)

func init() {
	Register(DisabledFeaturesCheck{})
	Register(FsyncCheck{})
	Register(SharedBuffersCheck{})
	Register(StatsCheck{})
	Register(WorkMemCheck{})
}

// Named thresholds. All are package-authored operator config — never a
// literal read from the monitored database.
const (
	sharedBuffersDefault int64 = 128 << 20 // 128 MiB — Postgres default / undersized
	workMemDefault       int64 = 4 << 20   // 4 MiB — Postgres default
	defaultStatsTarget   int64 = 100       // Postgres default_statistics_target default
)

// byName returns the allowlisted setting with the given name.
func byName(in *Input, name string) (SettingInfo, bool) {
	for _, s := range in.Settings {
		if s.Name == name {
			return s, true
		}
	}
	return SettingInfo{}, false
}

// settingBool reports whether a pg_settings boolean GUC is enabled.
func settingBool(value string) bool { return strings.TrimSpace(value) == "on" }

// settingBytes converts a pg_settings numeric value + unit into bytes.
// ok is false if the value is unparseable or the unit is unrecognized.
func settingBytes(value, unit string) (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, false
	}
	var mult int64
	switch unit {
	case "", "B":
		mult = 1
	case "kB":
		mult = 1 << 10
	case "8kB":
		mult = 8 << 10
	case "16kB":
		mult = 16 << 10
	case "MB":
		mult = 1 << 20
	case "GB":
		mult = 1 << 30
	default:
		return 0, false
	}
	return v * mult, true
}

// boolFlagRule flags a GUC when bad(value) holds. Detail is a package-authored
// string; Object is the fixed GUC name — no monitored-DB literal.
type boolFlagRule struct {
	name   string
	bad    func(string) bool
	sev    Severity
	detail string
}

func evalFlags(in *Input, checkID string, rules []boolFlagRule) []Result {
	var out []Result
	for _, r := range rules {
		s, ok := byName(in, r.name)
		if !ok || !r.bad(s.Value) {
			continue
		}
		out = append(out, Result{
			CheckID:  checkID,
			Category: "settings",
			Severity: r.sev,
			Status:   StatusFiring,
			Object:   r.name,
			Detail:   r.detail,
		})
	}
	return out
}

func off(v string) bool { return !settingBool(v) }

// DisabledFeaturesCheck flags tracking/maintenance features that break
// autovacuum, the planner statistics, or live-activity visibility when
// turned off. Bools only — T1.
type DisabledFeaturesCheck struct{}

func (DisabledFeaturesCheck) ID() string       { return "settings.disabled_features" }
func (DisabledFeaturesCheck) Category() string { return "settings" }

func (DisabledFeaturesCheck) Eval(in *Input) []Result {
	return evalFlags(in, "settings.disabled_features", []boolFlagRule{
		{"autovacuum", off, SeverityCritical,
			"autovacuum is off — dead tuples and transaction-id wraparound are no longer contained; re-enable immediately"},
		{"track_counts", off, SeverityCritical,
			"track_counts is off — autovacuum cannot trigger and the planner loses row-count statistics; re-enable"},
		{"track_activities", off, SeverityWarning,
			"track_activities is off — pg_stat_activity shows no live query state, blinding monitoring"},
	})
}

// FsyncCheck flags durability GUCs set to unsafe values that risk data loss
// or on-disk corruption. Bools/enum keyword only — T1.
type FsyncCheck struct{}

func (FsyncCheck) ID() string       { return "settings.fsync" }
func (FsyncCheck) Category() string { return "settings" }

func (FsyncCheck) Eval(in *Input) []Result {
	return evalFlags(in, "settings.fsync", []boolFlagRule{
		{"fsync", off, SeverityCritical,
			"fsync is off — an OS crash can silently corrupt the entire cluster; only ever acceptable on disposable data"},
		{"full_page_writes", off, SeverityCritical,
			"full_page_writes is off — a crash mid-write can leave torn pages that recovery cannot repair"},
		// Only the fully async value "off" is flagged; local/remote_* remain durable.
		{"synchronous_commit", func(v string) bool { return strings.TrimSpace(v) == "off" }, SeverityWarning,
			"synchronous_commit is off — committed transactions can be lost in a crash within the flush window"},
	})
}

// SharedBuffersCheck flags a shared_buffers at or below the Postgres default,
// which is undersized for most dedicated instances. Absolute threshold — it
// deliberately does not need a host-RAM signal. Bytes/const only — T1.
type SharedBuffersCheck struct{}

func (SharedBuffersCheck) ID() string       { return "settings.shared_buffers" }
func (SharedBuffersCheck) Category() string { return "settings" }

func (SharedBuffersCheck) Eval(in *Input) []Result {
	s, ok := byName(in, "shared_buffers")
	if !ok {
		return nil
	}
	b, ok := settingBytes(s.Value, s.Unit)
	if !ok || b > sharedBuffersDefault {
		return nil
	}
	return []Result{{
		CheckID:  "settings.shared_buffers",
		Category: "settings",
		Severity: SeverityWarning,
		Status:   StatusFiring,
		Object:   "shared_buffers",
		Detail: fmt.Sprintf(
			"shared_buffers is %d bytes (<= %d default/undersized); size to a meaningful fraction of host memory",
			b, sharedBuffersDefault),
	}}
}

// StatsCheck flags statistics settings that degrade plan quality: a coarse
// default_statistics_target and disabled per-node I/O timing. Counts/bools
// and package thresholds only — T1. (track_counts belongs to
// DisabledFeaturesCheck to avoid double-firing.)
type StatsCheck struct{}

func (StatsCheck) ID() string       { return "settings.stats" }
func (StatsCheck) Category() string { return "settings" }

func (StatsCheck) Eval(in *Input) []Result {
	var out []Result
	if s, ok := byName(in, "default_statistics_target"); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(s.Value), 10, 64); err == nil && n < defaultStatsTarget {
			out = append(out, Result{
				CheckID:  "settings.stats",
				Category: "settings",
				Severity: SeverityInfo,
				Status:   StatusFiring,
				Object:   "default_statistics_target",
				Detail: fmt.Sprintf(
					"default_statistics_target is %d (< %d) — planner histograms are coarse, hurting row estimates",
					n, defaultStatsTarget),
			})
		}
	}
	if s, ok := byName(in, "track_io_timing"); ok && off(s.Value) {
		out = append(out, Result{
			CheckID:  "settings.stats",
			Category: "settings",
			Severity: SeverityInfo,
			Status:   StatusFiring,
			Object:   "track_io_timing",
			Detail:   "track_io_timing is off — plans carry no per-node I/O timing, limiting slow-scan analysis",
		})
	}
	return out
}

// WorkMemCheck flags a work_mem at or below the Postgres default, at which
// large sorts and hashes spill to disk. Bytes/const only — T1.
type WorkMemCheck struct{}

func (WorkMemCheck) ID() string       { return "settings.work_mem" }
func (WorkMemCheck) Category() string { return "settings" }

func (WorkMemCheck) Eval(in *Input) []Result {
	s, ok := byName(in, "work_mem")
	if !ok {
		return nil
	}
	b, ok := settingBytes(s.Value, s.Unit)
	if !ok || b > workMemDefault {
		return nil
	}
	return []Result{{
		CheckID:  "settings.work_mem",
		Category: "settings",
		Severity: SeverityInfo,
		Status:   StatusFiring,
		Object:   "work_mem",
		Detail: fmt.Sprintf(
			"work_mem is %d bytes (<= %d default); large sorts and hashes spill to disk",
			b, workMemDefault),
	}}
}
