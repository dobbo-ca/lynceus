// Package ingest is the websocket receiver for collector snapshots.
// It validates the bearer token, applies a per-server token-bucket
// rate limit, writes accepted snapshots to the stats DB, and parks
// anything it cannot accept (rate-limited, malformed, write error)
// into the dead-letter queue for later retry.
package ingest

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/proto"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// Config is the ingestion server's runtime configuration.
type Config struct {
	// DevToken is the bearer token a collector must present. Empty
	// string disables auth — only acceptable in tests.
	DevToken string
	// RateLimit is the steady-state snapshots-per-second allowed per
	// monitored server.
	RateLimit float64
	// RateBurst is the burst capacity per monitored server.
	RateBurst int
	// ReadTimeout caps a single websocket read.
	ReadTimeout time.Duration
}

// Server is the websocket receiver.
type Server struct {
	cfg           Config
	stats         *store.Stats
	schemaObjects *store.SchemaObjects
	pool          *pgxpool.Pool

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

// NewServer returns a Server. pool is the stats-DB pool (used for the
// DLQ table and the schema_objects upsert); stats is the typed writer.
func NewServer(cfg Config, stats *store.Stats, pool *pgxpool.Pool) *Server {
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 30 * time.Second
	}
	if cfg.RateBurst == 0 {
		cfg.RateBurst = 1
	}
	return &Server{
		cfg:           cfg,
		stats:         stats,
		schemaObjects: store.NewSchemaObjects(pool),
		pool:          pool,
		limiters:      map[string]*rate.Limiter{},
	}
}

// Handler returns the HTTP handler that upgrades incoming connections
// to a websocket and processes one Snapshot per connection.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.handle)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if s.cfg.DevToken != "" {
		if r.Header.Get("Authorization") != "Bearer "+s.cfg.DevToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ReadTimeout)
	defer cancel()

	_, data, err := conn.Read(ctx)
	if err != nil {
		return
	}

	var snap lynceusv1.Snapshot
	if err := proto.Unmarshal(data, &snap); err != nil {
		s.parkDLQ(ctx, "", "unmarshal: "+err.Error(), data)
		_ = conn.Close(websocket.StatusInvalidFramePayloadData, "bad proto")
		return
	}

	if !s.limiterFor(snap.ServerId).Allow() {
		s.parkDLQ(ctx, snap.ServerId, "rate_limited", data)
		_ = conn.Close(websocket.StatusTryAgainLater, "rate limited")
		return
	}

	rows := snapshotToRows(&snap)
	if err := s.stats.WriteQueryStats(ctx, rows); err != nil {
		s.parkDLQ(ctx, snap.ServerId, "write: "+err.Error(), data)
		_ = conn.Close(websocket.StatusInternalError, "")
		return
	}
	if buckets := snapshotToActivityBuckets(&snap); len(buckets) > 0 {
		if err := s.stats.WriteActivityBuckets(ctx, buckets); err != nil {
			s.parkDLQ(ctx, snap.ServerId, "write activity: "+err.Error(), data)
			_ = conn.Close(websocket.StatusInternalError, "")
			return
		}
	}
	if plans := snapshotToQueryPlans(&snap); len(plans) > 0 {
		if err := s.stats.WriteQueryPlans(ctx, plans); err != nil {
			s.parkDLQ(ctx, snap.ServerId, "write plans: "+err.Error(), data)
			_ = conn.Close(websocket.StatusInternalError, "")
			return
		}
	}
	if objs := snapshotToSchemaObjects(&snap); len(objs) > 0 {
		if err := s.schemaObjects.UpsertSchemaObjects(ctx, objs); err != nil {
			s.parkDLQ(ctx, snap.ServerId, "write schema_objects: "+err.Error(), data)
			_ = conn.Close(websocket.StatusInternalError, "")
			return
		}
	}
	if ts := snapshotToTableStats(&snap); len(ts) > 0 {
		if err := s.stats.WriteTableStats(ctx, ts); err != nil {
			s.parkDLQ(ctx, snap.ServerId, "write table_stats: "+err.Error(), data)
			_ = conn.Close(websocket.StatusInternalError, "")
			return
		}
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func (s *Server) limiterFor(serverID string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.limiters[serverID]; ok {
		return l
	}
	l := rate.NewLimiter(rate.Limit(s.cfg.RateLimit), s.cfg.RateBurst)
	s.limiters[serverID] = l
	return l
}

func (s *Server) parkDLQ(ctx context.Context, serverID, reason string, raw []byte) {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO dlq (server_id, reason, raw)
		 VALUES (NULLIF($1, ''), $2, $3)`,
		serverID, reason, raw,
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("ingest: dlq insert failed: %v", err)
	}
}

func snapshotToRows(snap *lynceusv1.Snapshot) []store.QueryStat {
	collectedAt := time.Unix(snap.CollectedAtUnix, 0).UTC()
	if collectedAt.IsZero() || snap.CollectedAtUnix == 0 {
		collectedAt = time.Now().UTC()
	}
	rows := make([]store.QueryStat, 0, len(snap.QueryStats))
	for _, q := range snap.QueryStats {
		rows = append(rows, store.QueryStat{
			ServerID:        snap.ServerId,
			CollectedAt:     collectedAt,
			Fingerprint:     q.Fingerprint,
			NormalizedQuery: q.NormalizedQuery,
			DataTier:        1,
			Calls:           q.Calls,
			TotalTimeMs:     q.TotalTimeMs,
			MeanTimeMs:      q.MeanTimeMs,
			Rows:            q.Rows,
			SharedBlksHit:   q.SharedBlksHit,
			SharedBlksRead:  q.SharedBlksRead,
		})
	}
	return rows
}

func snapshotToQueryPlans(snap *lynceusv1.Snapshot) []store.QueryPlanRow {
	out := make([]store.QueryPlanRow, 0, len(snap.QueryPlans))
	for _, p := range snap.QueryPlans {
		out = append(out, store.QueryPlanRow{
			ServerID:   snap.ServerId,
			CapturedAt: time.Unix(p.CapturedAtUnix, 0).UTC(),
			Plan:       p,
			DataTier:   1,
		})
	}
	return out
}

// snapshotToSchemaObjects maps the T1 SchemaObject inventory onto the
// store row type. first_seen_at is resolved server-side by the upsert
// (ON CONFLICT preserves it), so the collector-supplied objects carry
// no first-seen — see internal/store/schema_objects.go.
func snapshotToSchemaObjects(snap *lynceusv1.Snapshot) []store.SchemaObjectRow {
	out := make([]store.SchemaObjectRow, 0, len(snap.SchemaObjects))
	for _, o := range snap.SchemaObjects {
		out = append(out, store.SchemaObjectRow{
			ServerID:    snap.ServerId,
			Kind:        int16(o.Kind),
			FQN:         o.Fqn,
			SchemaName:  o.Schema,
			ObjectName:  o.Name,
			SizeBytes:   o.SizeBytes,
			IsPartition: o.IsPartition,
			ParentFQN:   o.ParentFqn,
		})
	}
	return out
}

func snapshotToTableStats(snap *lynceusv1.Snapshot) []store.TableStatRow {
	collectedAt := time.Unix(snap.CollectedAtUnix, 0).UTC()
	if collectedAt.IsZero() || snap.CollectedAtUnix == 0 {
		collectedAt = time.Now().UTC()
	}
	unixToTime := func(u int64) time.Time {
		if u == 0 {
			return time.Time{}
		}
		return time.Unix(u, 0).UTC()
	}
	out := make([]store.TableStatRow, 0, len(snap.TableStats))
	for _, t := range snap.TableStats {
		out = append(out, store.TableStatRow{
			ServerID:    snap.ServerId,
			CollectedAt: collectedAt,
			SchemaName:  t.Schema,
			ObjectName:  t.Name,
			FQN:         t.Fqn,

			TotalBytes:   t.TotalBytes,
			HeapBytes:    t.HeapBytes,
			ToastBytes:   t.ToastBytes,
			IndexesBytes: t.IndexesBytes,

			RowEstimate:      t.RowEstimate,
			LiveTuples:       t.LiveTuples,
			DeadTuples:       t.DeadTuples,
			NModSinceAnalyze: t.NModSinceAnalyze,

			SeqScan:    t.SeqScan,
			IdxScan:    t.IdxScan,
			NTupIns:    t.NTupIns,
			NTupUpd:    t.NTupUpd,
			NTupDel:    t.NTupDel,
			NTupHotUpd: t.NTupHotUpd,

			LastVacuum:      unixToTime(t.LastVacuumUnix),
			LastAutovacuum:  unixToTime(t.LastAutovacuumUnix),
			LastAnalyze:     unixToTime(t.LastAnalyzeUnix),
			LastAutoanalyze: unixToTime(t.LastAutoanalyzeUnix),
			VacuumCount:     t.VacuumCount,
			AutovacuumCount: t.AutovacuumCount,

			DataTier: 1,
		})
	}
	return out
}

func snapshotToActivityBuckets(snap *lynceusv1.Snapshot) []store.ActivityBucket {
	out := make([]store.ActivityBucket, 0, len(snap.ActivityBuckets))
	for _, b := range snap.ActivityBuckets {
		out = append(out, store.ActivityBucket{
			ServerID:      snap.ServerId,
			Database:      b.DatabaseName,
			State:         b.State,
			WaitEventType: b.WaitEventType,
			WaitEvent:     b.WaitEvent,
			BucketStart:   time.Unix(b.BucketStartUnix, 0).UTC(),
			BucketSeconds: b.BucketSeconds,
			SampleCount:   b.SampleCount,
			CountSum:      b.CountSum,
			CountMax:      b.CountMax,
			DataTier:      1,
		})
	}
	return out
}
