// Package fleetview assembles UI view-models that span both stores: cluster
// topology lives in the config DB (store.Config) while metrics live in the
// stats DB (store.Stats), so the roll-up across a cluster's server streams is
// done here in Go rather than in a single SQL join.
package fleetview

import (
	"context"
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

		out = append(out, sum)
	}
	return out, nil
}
