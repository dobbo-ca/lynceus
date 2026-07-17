package store

import (
	"context"
	"time"
)

// Stats is the reader/writer seam over the time-series stats database.
type Stats interface {
	WriteQueryStats(ctx context.Context, rows []QueryStat) error
	TopQueriesByTotalTime(ctx context.Context, since, until time.Time, limit int) ([]TopQuery, error)
	WaitEventHistogram(ctx context.Context, serverID string, since, until time.Time) ([]WaitEventCount, error)
	WriteActivityBuckets(ctx context.Context, rows []ActivityBucket) error
	RecentServerIDs(ctx context.Context, since time.Time) ([]string, error)
	ThroughputForServers(ctx context.Context, serverIDs []string, since, until time.Time) (Throughput, error)
	TopQueriesForServers(ctx context.Context, serverIDs []string, since, until time.Time, limit int) ([]TopQuery, error)
	QPSBucketsForServers(ctx context.Context, serverIDs []string, since, until time.Time) ([]QPSBucket, error)
	ActivitySummaryForServers(ctx context.Context, serverIDs []string, since, until time.Time) (ActivitySummary, error)
	WriteQueryPlans(ctx context.Context, rows []QueryPlanRow) error
	TopPlansByQuery(ctx context.Context, serverID, fingerprint string, since, until time.Time, limit int) ([]QueryPlanRow, error)
	ListPlanKeys(ctx context.Context, since, until time.Time, limit int) ([]PlanKey, error)
	ReadQueryStatsTier2(ctx context.Context, serverID string, since, until time.Time, limit int) ([]QueryStat, error)
	WriteInsights(ctx context.Context, rows []InsightRow) error
	InsightCountForServers(ctx context.Context, serverIDs []string, since, until time.Time) (int, error)
	TopInsightsForServers(ctx context.Context, serverIDs []string, since, until time.Time, limit int) ([]InsightRow, error)
	WriteTableStats(ctx context.Context, rows []TableStatRow) error
	LatestTableStats(ctx context.Context, serverID string, asOf time.Time) ([]TableStatRow, error)
	WriteIndexStats(ctx context.Context, rows []IndexStatRow) error
	LatestIndexStats(ctx context.Context, serverID string, asOf time.Time) ([]IndexStatRow, error)
	WriteFreezeAges(ctx context.Context, rows []FreezeAgeRow) error
	LatestFreezeAges(ctx context.Context, serverID string, asOf time.Time) ([]FreezeAgeRow, error)
	WriteXminHorizons(ctx context.Context, rows []XminHorizonRow) error
	LatestXminHorizon(ctx context.Context, serverID string, asOf time.Time) (XminHorizonRow, bool, error)
	WriteSettings(ctx context.Context, rows []SettingRow) error
	LatestSettings(ctx context.Context, serverID string, asOf time.Time) ([]SettingRow, error)
	WriteConnectionSamples(ctx context.Context, rows []ConnectionSampleRow) error
	WriteBlockingEdges(ctx context.Context, rows []BlockingEdgeRow) error
	LatestConnectionSamples(ctx context.Context, serverID string, asOf time.Time) ([]ConnectionSampleRow, error)
	LatestBlockingEdges(ctx context.Context, serverID string, asOf time.Time) ([]BlockingEdgeRow, error)
	WriteChecksResults(ctx context.Context, rows []ChecksResultRow) error
	LatestChecksResults(ctx context.Context, serverID string, since, until time.Time) ([]ChecksResultRow, error)
	SetMute(ctx context.Context, serverID, checkID, object string, until time.Time, reason string) error
	ClearMute(ctx context.Context, serverID, checkID, object string) error
	ListMutes(ctx context.Context, serverID string) ([]MuteRow, error)
	WriteLogEvents(ctx context.Context, rows []LogEventRow) error
	ParkDLQ(ctx context.Context, serverID, reason string, raw []byte) error
	WriteSchemaObjects(ctx context.Context, rows []SchemaObjectRow) error
}

// QueryStat is one T1 row of per-fingerprint query statistics.
// DataTier zero is treated as 1 (T1) on insert — see package comment.
type QueryStat struct {
	ServerID        string
	CollectedAt     time.Time
	Fingerprint     string
	NormalizedQuery string
	// RawQuery is the literal-bearing raw query text. Populated ONLY on T2 rows
	// (DataTier==2) written to query_stats_t2; empty on T1. The normalization MV
	// projects the literal-free columns to query_stats and EXCLUDES this one.
	RawQuery       string
	DataTier       int16
	Calls          int64
	TotalTimeMs    float64
	MeanTimeMs     float64
	Rows           int64
	SharedBlksHit  int64
	SharedBlksRead int64
}

// TopQuery is one row returned by TopQueriesByTotalTime.
type TopQuery struct {
	Fingerprint     string
	NormalizedQuery string
	Calls           int64
	TotalTimeMs     float64
}

// ActivityBucket is one row of per-bucket connection-state histogram
// data. Labels and aggregate counts only — see the package comment and
// docs/specs/2026-05-29-lynceus-design.md §2.
type ActivityBucket struct {
	ServerID      string
	Database      string
	State         string
	WaitEventType string
	WaitEvent     string
	BucketStart   time.Time
	BucketSeconds int32
	SampleCount   int32
	CountSum      int64
	CountMax      int64
	DataTier      int16
}

// WaitEventCount is one aggregated wait-event class over a time window: a
// (type, event) label pair and the summed sample count. Empty type/event means
// the backend was active on CPU (no wait). T1 — labels + a count only.
type WaitEventCount struct {
	WaitEventType string
	WaitEvent     string
	Total         int64
	Buckets       int64 // how many buckets contributed (sampling depth)
}
