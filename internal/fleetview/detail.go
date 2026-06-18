package fleetview

import (
	"context"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// InstanceTopo holds topology + rolled-up metrics for one instance in the cluster.
type InstanceTopo struct {
	Instance    store.Instance
	Streams     []store.ServerStream
	Calls       int64 // window calls across this instance's streams
	ActiveConns int64
}

// ClusterDetail is the Overview page view-model for a single cluster.
type ClusterDetail struct {
	Cluster      store.Cluster
	Instances    []InstanceTopo
	StreamCount  int
	Calls        int64
	AvgLatencyMs float64
	ActiveConns  int64
	TopWait      string
	InsightCount int
	QPSBuckets   []store.QPSBucket
	TopQueries   []store.TopQuery   // limit 20
	Insights     []store.InsightRow // limit 50
}

// GetClusterDetail assembles a single cluster's Overview data. found=false (no
// error) if clusterID is unknown. Mirrors ListClusterSummaries' roll-up.
func GetClusterDetail(
	ctx context.Context, cfg *store.Config, stats *store.Stats,
	clusterID string, since, until time.Time,
) (ClusterDetail, bool, error) {
	clusters, err := cfg.ListClusters(ctx)
	if err != nil {
		return ClusterDetail{}, false, err
	}

	var cluster store.Cluster
	var found bool
	for _, cl := range clusters {
		if cl.ID == clusterID {
			cluster = cl
			found = true
			break
		}
	}
	if !found {
		return ClusterDetail{}, false, nil
	}

	serverIDs, err := cfg.ServerIDsForCluster(ctx, clusterID)
	if err != nil {
		return ClusterDetail{}, false, err
	}

	instances, err := cfg.ListInstances(ctx, clusterID)
	if err != nil {
		return ClusterDetail{}, false, err
	}

	topos := make([]InstanceTopo, 0, len(instances))
	for i := range instances {
		inst := instances[i]
		streams, err := cfg.ListServerStreams(ctx, inst.ID)
		if err != nil {
			return ClusterDetail{}, false, err
		}
		ids, err := cfg.ServerIDsForInstance(ctx, inst.ID)
		if err != nil {
			return ClusterDetail{}, false, err
		}
		topo := InstanceTopo{Instance: inst, Streams: streams}
		if len(ids) > 0 {
			tp, err := stats.ThroughputForServers(ctx, ids, since, until)
			if err != nil {
				return ClusterDetail{}, false, err
			}
			topo.Calls = tp.Calls
			act, err := stats.ActivitySummaryForServers(ctx, ids, since, until)
			if err != nil {
				return ClusterDetail{}, false, err
			}
			topo.ActiveConns = act.ActiveConns
		}
		topos = append(topos, topo)
	}

	detail := ClusterDetail{
		Cluster:     cluster,
		Instances:   topos,
		StreamCount: len(serverIDs),
	}
	if len(serverIDs) == 0 {
		return detail, true, nil
	}

	tp, err := stats.ThroughputForServers(ctx, serverIDs, since, until)
	if err != nil {
		return ClusterDetail{}, false, err
	}
	detail.Calls = tp.Calls
	if tp.Calls > 0 {
		detail.AvgLatencyMs = tp.TotalTimeMs / float64(tp.Calls)
	}

	if detail.QPSBuckets, err = stats.QPSBucketsForServers(ctx, serverIDs, since, until); err != nil {
		return ClusterDetail{}, false, err
	}

	act, err := stats.ActivitySummaryForServers(ctx, serverIDs, since, until)
	if err != nil {
		return ClusterDetail{}, false, err
	}
	detail.ActiveConns = act.ActiveConns
	detail.TopWait = act.TopWait

	if detail.InsightCount, err = stats.InsightCountForServers(ctx, serverIDs, since, until); err != nil {
		return ClusterDetail{}, false, err
	}

	if detail.TopQueries, err = stats.TopQueriesForServers(ctx, serverIDs, since, until, 20); err != nil {
		return ClusterDetail{}, false, err
	}

	if detail.Insights, err = stats.TopInsightsForServers(ctx, serverIDs, since, until, 50); err != nil {
		return ClusterDetail{}, false, err
	}

	return detail, true, nil
}
