package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// QueryPlanRow is one extracted auto_explain plan as stored in the stats DB.
// Plan carries the normalized (literal-free) T1 tree; it is marshaled to the
// plan_tree JSONB column. DataTier zero is treated as 1 (T1) on insert.
type QueryPlanRow struct {
	ServerID   string
	CapturedAt time.Time
	Plan       *lynceusv1.QueryPlan
	DataTier   int16
}

// queryPlansColumns is the COPY column order for WriteQueryPlans.
var queryPlansColumns = []string{
	"server_id", "fingerprint", "captured_at", "format_version",
	"total_cost", "actual_total_time_ms", "plan_tree", "data_tier",
}

// WriteQueryPlans appends a batch of normalized plans via the COPY protocol,
// creating any missing weekly partitions first. Mirrors WriteQueryStats /
// WriteActivityBuckets: COPY routes each row to its weekly partition and is
// lighter on the storage DB than per-row INSERTs.
func (s *Stats) WriteQueryPlans(ctx context.Context, rows []QueryPlanRow) error {
	if len(rows) == 0 {
		return nil
	}

	weeks := map[string]time.Time{}
	for _, r := range rows {
		weeks[plansPartitionName(r.CapturedAt)] = r.CapturedAt
	}
	for _, ts := range weeks {
		if err := s.EnsurePlansWeeklyPartition(ctx, ts); err != nil {
			return err
		}
	}

	src := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		tree, err := protojson.Marshal(r.Plan.GetRoot())
		if err != nil {
			return nil, fmt.Errorf("marshal plan_tree: %w", err)
		}
		return []any{
			r.ServerID, r.Plan.GetFingerprint(), r.CapturedAt, r.Plan.GetFormatVersion(),
			r.Plan.GetTotalCost(), r.Plan.GetActualTotalTimeMs(), string(tree), r.DataTier,
		}, nil
	})
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"query_plans"}, queryPlansColumns, src)
	return err
}

// TopPlansByQuery returns up to limit plans for (serverID, fingerprint)
// captured in [since, until), most recent first. data_tier = 1 only (T1).
func (s *Stats) TopPlansByQuery(
	ctx context.Context, serverID, fingerprint string, since, until time.Time, limit int,
) ([]QueryPlanRow, error) {
	rows, err := s.ro.Query(ctx,
		`SELECT server_id, fingerprint, captured_at, format_version,
		        total_cost, actual_total_time_ms, plan_tree, data_tier
		   FROM query_plans
		  WHERE server_id = $1 AND fingerprint = $2
		    AND captured_at >= $3 AND captured_at < $4
		    AND data_tier = 1
		  ORDER BY captured_at DESC
		  LIMIT $5`,
		serverID, fingerprint, since, until, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []QueryPlanRow
	for rows.Next() {
		var (
			r    QueryPlanRow
			fp   string
			ver  int32
			tc   float64
			att  float64
			tree []byte
		)
		if err := rows.Scan(
			&r.ServerID, &fp, &r.CapturedAt, &ver, &tc, &att, &tree, &r.DataTier,
		); err != nil {
			return nil, err
		}
		root := &lynceusv1.PlanNode{}
		if err := protojson.Unmarshal(tree, root); err != nil {
			return nil, fmt.Errorf("unmarshal plan_tree: %w", err)
		}
		r.Plan = &lynceusv1.QueryPlan{
			Fingerprint:       fp,
			CapturedAtUnix:    r.CapturedAt.Unix(),
			FormatVersion:     ver,
			TotalCost:         tc,
			ActualTotalTimeMs: att,
			Root:              root,
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EnsurePlansWeeklyPartition creates the weekly partition for ts on
// query_plans if it does not already exist. Idempotent.
func (s *Stats) EnsurePlansWeeklyPartition(ctx context.Context, ts time.Time) error {
	name := plansPartitionName(ts)
	from, to := isoWeekBounds(ts)
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF query_plans
		 FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
	))
	return err
}

func plansPartitionName(ts time.Time) string {
	y, w := ts.UTC().ISOWeek()
	return fmt.Sprintf("query_plans_%04d_%02d", y, w)
}
