package store

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// chQueryPlansCols is the shared column order for query_plans writes and the
// TopPlansByQuery read. It mirrors queryPlansColumns (plans.go) so the CH and
// pgx backends persist identical fields. plan_tree holds the protojson of the
// normalized (literal-free) root PlanNode — the same serialized form the pgx
// backend writes to its JSONB column.
const chQueryPlansCols = "server_id, fingerprint, captured_at, format_version, " +
	"total_cost, actual_total_time_ms, plan_tree, data_tier"

// WriteQueryPlans batch-inserts normalized plans into query_plans. Mirrors the
// pgx WriteQueryPlans (plans.go): DataTier zero is normalized to 1 (T1), the
// root PlanNode is marshaled to plan_tree via protojson, and the hot scalar
// columns are pulled off the QueryPlan proto. Unlike WriteQueryStats there is
// no tier split — plan_tree is normalized, so every row lands in query_plans
// and TopPlansByQuery filters data_tier at read time.
func (s *chStats) WriteQueryPlans(ctx context.Context, rows []QueryPlanRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO query_plans ("+chQueryPlansCols+")")
	if err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		if r.DataTier == 0 {
			r.DataTier = 1
		}
		tree, err := protojson.Marshal(r.Plan.GetRoot())
		if err != nil {
			_ = batch.Abort()
			return fmt.Errorf("marshal plan_tree: %w", err)
		}
		if err := batch.Append(
			r.ServerID, r.Plan.GetFingerprint(), r.CapturedAt, r.Plan.GetFormatVersion(),
			r.Plan.GetTotalCost(), r.Plan.GetActualTotalTimeMs(), string(tree), r.DataTier,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// TopPlansByQuery returns up to limit plans for (serverID, fingerprint)
// captured in [since, until), most recent first. data_tier = 1 only (T1).
// Mirrors the pgx TopPlansByQuery (plans.go), reconstructing the QueryPlan
// proto from the scalar columns plus the deserialized plan_tree root.
func (s *chStats) TopPlansByQuery(
	ctx context.Context, serverID, fingerprint string, since, until time.Time, limit int,
) ([]QueryPlanRow, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT `+chQueryPlansCols+`
		   FROM query_plans
		  WHERE server_id = ? AND fingerprint = ?
		    AND captured_at >= ? AND captured_at < ?
		    AND data_tier = 1
		  ORDER BY captured_at DESC
		  LIMIT ?`,
		serverID, fingerprint, since, until, uint64(limit),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []QueryPlanRow
	for rows.Next() {
		var (
			r    QueryPlanRow
			fp   string
			ver  int32
			tc   float64
			att  float64
			tree string
		)
		if err := rows.Scan(
			&r.ServerID, &fp, &r.CapturedAt, &ver, &tc, &att, &tree, &r.DataTier,
		); err != nil {
			return nil, err
		}
		root := &lynceusv1.PlanNode{}
		if err := protojson.Unmarshal([]byte(tree), root); err != nil {
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

// ListPlanKeys enumerates the distinct (server_id, fingerprint) keys that have
// at least one plan captured in [since, until), in server/fingerprint order, up
// to limit. data_tier = 1 only (T1). Mirrors the pgx ListPlanKeys (plans.go).
func (s *chStats) ListPlanKeys(
	ctx context.Context, since, until time.Time, limit int,
) ([]PlanKey, error) {
	rows, err := s.conn.Query(ctx,
		`SELECT DISTINCT server_id, fingerprint
		   FROM query_plans
		  WHERE captured_at >= ? AND captured_at < ?
		    AND data_tier = 1
		  ORDER BY server_id, fingerprint
		  LIMIT ?`,
		since, until, uint64(limit),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []PlanKey
	for rows.Next() {
		var k PlanKey
		if err := rows.Scan(&k.ServerID, &k.Fingerprint); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
