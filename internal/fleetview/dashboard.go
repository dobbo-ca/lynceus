package fleetview

import (
	"context"
	"sort"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// Sev is the normalized 3-band severity used across the dashboard.
type Sev string

const (
	SevCrit Sev = "crit"
	SevWarn Sev = "warn"
	SevInfo Sev = "info"
)

// normSev folds the checks vocabulary {info,warning,critical} and the insights
// vocabulary {low,medium,high} onto the three dashboard bands. Unknown -> info.
func normSev(raw string) Sev {
	switch raw {
	case "critical", "high":
		return SevCrit
	case "warning", "medium":
		return SevWarn
	default:
		return SevInfo
	}
}

func sevRank(s Sev) int {
	switch s {
	case SevCrit:
		return 0
	case SevWarn:
		return 1
	default:
		return 2
	}
}

// AttentionItem is one open check or insight surfaced in the fleet
// Needs-Attention list. All fields are T1.
type AttentionItem struct {
	Kind        string
	ID          string
	Detail      string
	Sev         Sev
	ServerID    string
	ServerName  string
	ClusterID   string
	Category    string
	CheckID     string
	Fingerprint string
	At          time.Time
}

// FleetCluster is one cluster's dashboard roll-up.
type FleetCluster struct {
	ClusterID    string
	Name         string
	Version      string
	Provider     string
	ProviderName string
	Engine       string
	EngineIcon   string
	Health       string
	HealthSev    Sev
	Crit         int
	Warn         int
	Info         int
	QPS          float64
	LatencyMs    float64
	ActiveConns  int64
	TopWait      string
}

// FleetView is the whole dashboard domain model (pre-presentation).
type FleetView struct {
	Clusters      []FleetCluster
	Attention     []AttentionItem
	OpenCrit      int
	OpenWarn      int
	OpenInfo      int
	ClusterCount  int
	NodeCount     int
	DatabaseCount int
	Healthy       bool
}

// BuildFleetView assembles the fleet dashboard: per-cluster metrics (reusing
// ListClusterSummaries) fused with open-check + insight severity roll-ups and a
// fleet-wide, severity-sorted Needs-Attention list. Only T1 data is read.
func BuildFleetView(
	ctx context.Context, cfg store.Config, stats store.Stats, since, until time.Time,
) (FleetView, error) {
	summaries, err := ListClusterSummaries(ctx, cfg, stats, since, until)
	if err != nil {
		return FleetView{}, err
	}

	var fv FleetView
	fv.ClusterCount = len(summaries)

	for i := range summaries {
		sum := &summaries[i]
		fv.NodeCount += sum.InstanceCount

		serverIDs, err := cfg.ServerIDsForCluster(ctx, sum.Cluster.ID)
		if err != nil {
			return FleetView{}, err
		}
		instances, err := cfg.ListInstances(ctx, sum.Cluster.ID)
		if err != nil {
			return FleetView{}, err
		}
		// serverID -> node (instance) display name; also collect distinct
		// database names so the fleet DATABASES stat counts DISTINCT
		// (cluster, database_name) — the same definition the Databases screen
		// (ListDatabaseGroups) uses — rather than the raw server-stream count.
		nameByServer := map[string]string{}
		dbNames := map[string]struct{}{}
		for j := range instances {
			streams, err := cfg.ListServerStreams(ctx, instances[j].ID)
			if err != nil {
				return FleetView{}, err
			}
			for k := range streams {
				nameByServer[streams[k].ServerID] = instances[j].Name
				if name := streams[k].DatabaseName; name != "" {
					dbNames[name] = struct{}{}
				}
			}
		}
		fv.DatabaseCount += len(dbNames)
		nameOf := func(sid string) string {
			if n := nameByServer[sid]; n != "" {
				return n
			}
			return sid
		}

		fc := FleetCluster{
			ClusterID:   sum.Cluster.ID,
			Name:        sum.Cluster.Name,
			Engine:      "POSTGRESQL",
			EngineIcon:  "eng-pg",
			LatencyMs:   sum.AvgLatencyMs,
			ActiveConns: sum.ActiveConns,
			TopWait:     sum.TopWait,
		}
		if n := len(sum.QPSBuckets); n > 0 {
			fc.QPS = float64(sum.QPSBuckets[n-1].Calls) / 3600.0
		}

		// open checks (firing, not muted)
		for _, sid := range serverIDs {
			checks, err := stats.LatestChecksResults(ctx, sid, since, until)
			if err != nil {
				return FleetView{}, err
			}
			for c := range checks {
				ch := &checks[c]
				if ch.Status != "firing" || ch.Muted {
					continue
				}
				sev := normSev(ch.Severity)
				bump(&fc, sev)
				fv.Attention = append(fv.Attention, AttentionItem{
					Kind: "check", ID: ch.CheckID, Detail: ch.Detail, Sev: sev,
					ServerID: sid, ServerName: nameOf(sid), ClusterID: sum.Cluster.ID,
					Category: ch.Category, CheckID: ch.CheckID, At: ch.EvaluatedAt,
				})
			}
		}
		// insights (already T1-filtered by the store)
		insights, err := stats.TopInsightsForServers(ctx, serverIDs, since, until, 50)
		if err != nil {
			return FleetView{}, err
		}
		for r := range insights {
			in := &insights[r]
			sev := normSev(in.Severity)
			bump(&fc, sev)
			fv.Attention = append(fv.Attention, AttentionItem{
				Kind: "insight", ID: "insight: " + in.Kind, Detail: in.Detail, Sev: sev,
				ServerID: in.ServerID, ServerName: nameOf(in.ServerID), ClusterID: sum.Cluster.ID,
				Fingerprint: in.Fingerprint, At: in.CapturedAt,
			})
		}

		fc.Health, fc.HealthSev = deriveHealth(fc.Crit, fc.Warn)
		fv.OpenCrit += fc.Crit
		fv.OpenWarn += fc.Warn
		fv.OpenInfo += fc.Info
		fv.Clusters = append(fv.Clusters, fc)
	}

	sort.SliceStable(fv.Attention, func(a, b int) bool {
		ra, rb := sevRank(fv.Attention[a].Sev), sevRank(fv.Attention[b].Sev)
		if ra != rb {
			return ra < rb
		}
		return fv.Attention[a].At.After(fv.Attention[b].At)
	})
	fv.Healthy = len(fv.Attention) == 0
	return fv, nil
}

func bump(fc *FleetCluster, sev Sev) {
	switch sev {
	case SevCrit:
		fc.Crit++
	case SevWarn:
		fc.Warn++
	default:
		fc.Info++
	}
}

// deriveHealth: any crit -> DEGRADED; else any warn -> WARNING; else HEALTHY.
// Info-only clusters are HEALTHY (info advisories don't degrade health).
func deriveHealth(crit, warn int) (string, Sev) {
	switch {
	case crit > 0:
		return "DEGRADED", SevCrit
	case warn > 0:
		return "WARNING", SevWarn
	default:
		return "HEALTHY", SevInfo
	}
}
