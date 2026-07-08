package checks

import (
	"context"
	"log"
	"time"

	"github.com/dobbo-ca/lynceus/internal/advisor"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// schedulerLockKey is a fixed pg advisory-lock key so that across N
// ingestion replicas only one evaluates+persists per tick.
const schedulerLockKey int64 = 7426398501234599001

// Notifier receives non-muted firing results. Email/Slack (ly-7ck.5/.6)
// implement it; the default is a no-op.
type Notifier interface {
	Notify(ctx context.Context, r Result) error
}

// NopNotifier drops results.
type NopNotifier struct{}

func (NopNotifier) Notify(context.Context, Result) error { return nil }

// Scheduler periodically evaluates checks over the latest per-server stats
// and persists results. It runs in the ingestion service (write side).
type Scheduler struct {
	stats    store.Stats
	checks   []Check
	notify   Notifier
	interval time.Duration
	now      func() time.Time
}

// NewScheduler builds a Scheduler with the given store, checks, and
// notifier. interval defaults to 60s; now defaults to time.Now.
func NewScheduler(s store.Stats, cs []Check, n Notifier) *Scheduler {
	if n == nil {
		n = NopNotifier{}
	}
	return &Scheduler{stats: s, checks: cs, notify: n, interval: 60 * time.Second, now: time.Now}
}

func (sc *Scheduler) WithInterval(d time.Duration) *Scheduler {
	if d > 0 {
		sc.interval = d
	}
	return sc
}
func (sc *Scheduler) WithNow(f func() time.Time) *Scheduler {
	if f != nil {
		sc.now = f
	}
	return sc
}

// Run ticks RunOnce until ctx is cancelled. First tick fires immediately.
func (sc *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(sc.interval)
	defer t.Stop()
	if err := sc.RunOnce(ctx); err != nil {
		log.Printf("checks: first run: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := sc.RunOnce(ctx); err != nil {
				log.Printf("checks: run: %v", err)
			}
		}
	}
}

// RunOnce evaluates every server once under the advisory lock. If the lock
// is held by another replica it returns nil (that replica owns this tick).
func (sc *Scheduler) RunOnce(ctx context.Context) error {
	conn, err := sc.stats.Pool().Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, schedulerLockKey).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() { _, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, schedulerLockKey) }()

	now := sc.now().UTC()
	servers, err := sc.stats.RecentServerIDs(ctx, now.AddDate(0, 0, -1))
	if err != nil {
		return err
	}
	for _, srv := range servers {
		in, err := sc.assembleInput(ctx, srv, now)
		if err != nil {
			log.Printf("checks: assemble %s: %v", srv, err)
			continue
		}
		results := Run(&in, sc.checks)
		mutes, _ := sc.stats.ListMutes(ctx, srv)
		var rows []store.ChecksResultRow
		for _, r := range results {
			muted := isMuted(mutes, &r)
			rows = append(rows, store.ChecksResultRow{
				ServerID: r.ServerID, EvaluatedAt: now, CheckID: r.CheckID,
				Category: r.Category, Severity: string(r.Severity), Status: string(r.Status),
				Object: r.Object, Detail: r.Detail, Muted: muted,
			})
			if !muted && r.Status == StatusFiring {
				if err := sc.notify.Notify(ctx, r); err != nil {
					log.Printf("checks: notify %s/%s: %v", r.CheckID, r.Object, err)
				}
			}
		}
		if err := sc.stats.WriteChecksResults(ctx, rows); err != nil {
			return err
		}
	}
	return nil
}

func isMuted(mutes []store.MuteRow, r *Result) bool {
	for _, m := range mutes {
		if m.CheckID == r.CheckID && (m.Object == "" || m.Object == r.Object) {
			return true
		}
	}
	return false
}

// assembleInput reads the latest per-server stats the checks need. Part B
// extends it with FreezeAges.
func (sc *Scheduler) assembleInput(ctx context.Context, serverID string, now time.Time) (Input, error) {
	in := Input{ServerID: serverID, Now: now}
	tables, err := sc.stats.LatestTableStats(ctx, serverID, now)
	if err != nil {
		return in, err
	}
	for i := range tables {
		t := &tables[i]
		in.TableStats = append(in.TableStats, TableInfo{
			Relation: t.FQN, LiveTuples: t.LiveTuples, DeadTuples: t.DeadTuples,
			NModSinceAnalyze: t.NModSinceAnalyze, SeqScan: t.SeqScan, IdxScan: t.IdxScan,
		})
	}
	fz, err := sc.stats.LatestFreezeAges(ctx, serverID, now)
	if err != nil {
		return in, err
	}
	for i := range fz {
		f := &fz[i]
		in.FreezeAges = append(in.FreezeAges, FreezeInfo{
			Scope: f.Scope, Relation: f.FQN, XIDAge: f.XIDAge, MXIDAge: f.MXIDAge,
			AutovacuumFreezeMaxAge: f.AutovacuumFreezeMaxAge,
		})
	}

	if xh, ok, err := sc.stats.LatestXminHorizon(ctx, serverID, now); err != nil {
		return in, err
	} else if ok {
		in.XminHorizon = &XminInfo{OldestXminAge: xh.OldestXminAge, HolderKind: xh.HolderKind}
	}

	conns, err := sc.stats.LatestConnectionSamples(ctx, serverID, now)
	if err != nil {
		return in, err
	}
	for i := range conns {
		c := &conns[i]
		in.Connections = append(in.Connections, ConnInfo{
			PID: c.PID, State: c.State, ActiveSeconds: c.ActiveSeconds,
			XactSeconds: c.XactSeconds, StateSeconds: c.StateSeconds, WaitEventType: c.WaitEventType,
		})
	}
	edges, err := sc.stats.LatestBlockingEdges(ctx, serverID, now)
	if err != nil {
		return in, err
	}
	for i := range edges {
		e := &edges[i]
		in.Blocking = append(in.Blocking, BlockEdge{
			BlockedPID: e.BlockedPID, BlockerPID: e.BlockerPID, BlockedWaitSeconds: e.BlockedWaitSeconds,
		})
	}

	idxStats, err := sc.stats.LatestIndexStats(ctx, serverID, now)
	if err != nil {
		return in, err
	}
	for i := range idxStats {
		ix := &idxStats[i]
		in.Indexes = append(in.Indexes, IndexInfo{
			Schema: ix.SchemaName, Name: ix.ObjectName, FQN: ix.FQN, TableFQN: ix.TableFQN,
			IdxScan: ix.IdxScan, SizeBytes: ix.SizeBytes,
			IsValid: ix.IsValid, IsReady: ix.IsReady, IsUnique: ix.IsUnique, IsPrimary: ix.IsPrimary,
		})
	}

	// Index Advisor (ly-u4t.27): gather this server's recent plans + table
	// sizes and run the pure recommender, mirroring api.fetchIndexAdvice but
	// scoped to serverID. Reuses the `tables` rows already read above.
	since := now.AddDate(0, 0, -30)
	if idxKeys, err := sc.stats.ListPlanKeys(ctx, since, now, 200); err == nil {
		var plans []*lynceusv1.QueryPlan
		for _, k := range idxKeys {
			if k.ServerID != serverID {
				continue
			}
			ps, e := sc.stats.TopPlansByQuery(ctx, serverID, k.Fingerprint, since, now, 10)
			if e != nil {
				continue
			}
			for _, p := range ps {
				plans = append(plans, p.Plan)
			}
		}
		idxTables := map[string]advisor.TableInfo{}
		for i := range tables {
			t := &tables[i]
			ti := idxTables[t.ObjectName]
			ti.TotalBytes = t.TotalBytes
			ti.SeqScans = t.SeqScan
			idxTables[t.ObjectName] = ti
		}
		in.IndexRecs = advisor.RecommendIndexes(plans, idxTables)
	}

	return in, nil
}
