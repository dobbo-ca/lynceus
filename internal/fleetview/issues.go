package fleetview

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// ScopeIssue is one open check or insight attributed to a scope's server set,
// normalized for the "OPEN ISSUES ON THIS <scope>" list. Kind is "check" or
// "insight"; Ref is the check_id or the query fingerprint used to deep-link.
// Every field is already-normalized T1 data — no monitored-DB literal.
type ScopeIssue struct {
	Kind     string
	Severity string // normalized: "crit" | "warn" | "info"
	ID       string
	Detail   string
	Server   string
	Ref      string
	AgeMin   int
}

// ScopeIssues assembles the open issues for a server-id set over [since, until):
// firing, non-muted check results plus all insights, normalized to the design's
// crit/warn/info vocabulary and sorted crit>warn>info then newest first.
func ScopeIssues(
	ctx context.Context, stats store.Stats, serverIDs []string, since, until time.Time,
) ([]ScopeIssue, error) {
	var out []ScopeIssue
	for _, sid := range serverIDs {
		checks, err := stats.LatestChecksResults(ctx, sid, since, until)
		if err != nil {
			return nil, err
		}
		for i := range checks {
			c := &checks[i]
			if c.Muted || c.Status != "firing" {
				continue
			}
			out = append(out, ScopeIssue{
				Kind:     "check",
				Severity: NormalizeSeverity(c.Severity),
				ID:       c.CheckID,
				Detail:   c.Detail,
				Server:   sid,
				Ref:      c.CheckID,
				AgeMin:   ageMinutes(until, c.EvaluatedAt),
			})
		}
	}
	insights, err := stats.TopInsightsForServers(ctx, serverIDs, since, until, 50)
	if err != nil {
		return nil, err
	}
	for i := range insights {
		in := &insights[i]
		out = append(out, ScopeIssue{
			Kind:     "insight",
			Severity: NormalizeSeverity(in.Severity),
			ID:       fmt.Sprintf("%s · %s", in.Kind, in.Fingerprint),
			Detail:   in.Detail,
			Server:   in.ServerID,
			Ref:      in.Fingerprint,
			AgeMin:   ageMinutes(until, in.CapturedAt),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := scopeSevRank(out[i].Severity), scopeSevRank(out[j].Severity); ri != rj {
			return ri < rj
		}
		return out[i].AgeMin < out[j].AgeMin
	})
	return out, nil
}

func ageMinutes(until, t time.Time) int {
	m := int(until.Sub(t).Minutes())
	if m < 0 {
		return 0
	}
	return m
}

// NormalizeSeverity maps the engines' severities (checks: critical/warning/info;
// insights: high/medium/low) onto the design's crit/warn/info vocabulary.
func NormalizeSeverity(s string) string {
	switch strings.ToLower(s) {
	case "high", "critical", "crit":
		return "crit"
	case "medium", "warning", "warn":
		return "warn"
	default:
		return "info"
	}
}

func scopeSevRank(s string) int {
	switch s {
	case "crit":
		return 0
	case "warn":
		return 1
	default:
		return 2
	}
}

// WorstSeverity returns the highest-ranked normalized severity across issues, or
// "" when the set is empty. Used to derive a scope's single health modifier.
func WorstSeverity(issues []ScopeIssue) string {
	mod := ""
	for i := range issues {
		switch issues[i].Severity {
		case "crit":
			return "crit"
		case "warn":
			mod = "warn"
		case "info":
			if mod == "" {
				mod = "info"
			}
		}
	}
	return mod
}
