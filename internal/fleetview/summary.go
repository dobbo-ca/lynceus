// Package fleetview assembles UI view-models that span both stores: cluster
// topology lives in the config DB (store.Config) while metrics live in the
// stats DB (store.Stats), so the roll-up across a cluster's server streams is
// done here in Go rather than in a single SQL join.
package fleetview

import (
	"context"
	"strconv"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// ClusterSummary is the dashboard view-model for one cluster: its identity plus
// metrics rolled up across all of its server streams (combined).
type ClusterSummary struct {
	Cluster       store.Cluster
	InstanceCount int
	StreamCount   int
	Calls         int64             // total calls across the cluster in the window
	AvgLatencyMs  float64           // SUM(total_time_ms)/SUM(calls); 0 if no calls
	QPSBuckets    []store.QPSBucket // hourly summed calls, for the sparkline
	ActiveConns   int64
	TopWait       string
	InsightCount  int
	CritOpen      int    // firing, non-muted latest checks with severity=critical, across the cluster's servers
	WarnOpen      int    // severity=warning
	InfoOpen      int    // severity=info
	Version       string // display "major.minor" (e.g. "16.3") from server_version_num of the cluster's first server stream; "" if unknown
}

// rollupOpenChecks tallies firing, non-muted latest check results by severity
// across the server set. Severity strings are the stored check vocab
// ("critical"/"warning"/"info"); Status "firing" (not "ok") and !Muted == open.
func rollupOpenChecks(ctx context.Context, stats store.Stats, serverIDs []string, since, until time.Time) (crit, warn, info int, err error) {
	for _, sid := range serverIDs {
		rows, err := stats.LatestChecksResults(ctx, sid, since, until)
		if err != nil {
			return 0, 0, 0, err
		}
		for i := range rows {
			r := &rows[i]
			if r.Muted || r.Status != "firing" {
				continue
			}
			switch r.Severity {
			case "critical":
				crit++
			case "warning":
				warn++
			default:
				info++
			}
		}
	}
	return crit, warn, info, nil
}

// settingsForServer extracts the display version + max_connections from the
// server's latest curated pg_settings. Both source GUCs (server_version_num,
// max_connections) are in the collector allowlist; either may be absent
// (returns "" / 0) on a stream that has not reported settings yet.
//
// NOTE: version is derived from server_version_num (the integer GUC the
// collector actually ships), NOT the free-form server_version string — which is
// deliberately NOT allowlisted.
func settingsForServer(ctx context.Context, stats store.Stats, serverID string, asOf time.Time) (version string, maxConns int64, err error) {
	rows, err := stats.LatestSettings(ctx, serverID, asOf)
	if err != nil {
		return "", 0, err
	}
	for i := range rows {
		switch rows[i].Name {
		case "server_version_num":
			version = formatServerVersion(rows[i].Value)
		case "max_connections":
			if n, perr := strconv.ParseInt(rows[i].Value, 10, 64); perr == nil {
				maxConns = n
			}
		}
	}
	return version, maxConns, nil
}

// formatServerVersion turns a pg_settings server_version_num integer (e.g.
// "160003") into the display "major.minor" ("16.3"). Lynceus's supported
// baseline is PG 12+, where the encoding is major*10000 + minor, so integer
// division/modulo by 10000 is exact. Returns "" for a blank/unparseable value.
func formatServerVersion(raw string) string {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return ""
	}
	return strconv.Itoa(n/10000) + "." + strconv.Itoa(n%10000)
}

// ListClusterSummaries returns one summary per cluster, rolling stats up across
// each cluster's server_id set (resolved from the config DB).
func ListClusterSummaries(
	ctx context.Context, cfg store.Config, stats store.Stats, since, until time.Time,
) ([]ClusterSummary, error) {
	clusters, err := cfg.ListClusters(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]ClusterSummary, 0, len(clusters))
	for _, cl := range clusters {
		serverIDs, err := cfg.ServerIDsForCluster(ctx, cl.ID)
		if err != nil {
			return nil, err
		}
		instances, err := cfg.ListInstances(ctx, cl.ID)
		if err != nil {
			return nil, err
		}

		sum := ClusterSummary{
			Cluster:       cl,
			InstanceCount: len(instances),
			StreamCount:   len(serverIDs),
		}
		if len(serverIDs) == 0 {
			out = append(out, sum)
			continue
		}

		tp, err := stats.ThroughputForServers(ctx, serverIDs, since, until)
		if err != nil {
			return nil, err
		}
		sum.Calls = tp.Calls
		if tp.Calls > 0 {
			sum.AvgLatencyMs = tp.TotalTimeMs / float64(tp.Calls)
		}

		if sum.QPSBuckets, err = stats.QPSBucketsForServers(ctx, serverIDs, since, until); err != nil {
			return nil, err
		}

		act, err := stats.ActivitySummaryForServers(ctx, serverIDs, since, until)
		if err != nil {
			return nil, err
		}
		sum.ActiveConns = act.ActiveConns
		sum.TopWait = act.TopWait

		if sum.InsightCount, err = stats.InsightCountForServers(ctx, serverIDs, since, until); err != nil {
			return nil, err
		}

		if sum.CritOpen, sum.WarnOpen, sum.InfoOpen, err = rollupOpenChecks(ctx, stats, serverIDs, since, until); err != nil {
			return nil, err
		}
		if sum.Version, _, err = settingsForServer(ctx, stats, serverIDs[0], until); err != nil {
			return nil, err
		}

		out = append(out, sum)
	}
	return out, nil
}
