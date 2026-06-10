package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/collector"
	"github.com/dobbo-ca/lynceus/internal/logparse"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// main wires the collector's readers, tickers, and select loop; the branching
// is inherent orchestration, hence the gocyclo waiver.
//
//nolint:gocyclo // orchestration entrypoint; complexity is inherent
func main() {
	cfg := loadConfig()

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.pgDSN)
	if err != nil {
		log.Fatalf("connect monitored postgres: %v", err) //nolint:gocritic // exitAfterDefer: deferred cleanup is best-effort on a fatal process exit
	}
	defer pool.Close()

	// Resolve the monitored connection's database once — it is the gate key.
	var db string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		log.Fatalf("resolve current_database: %v", err)
	}

	gate := caps.NewGate()
	reader := collector.NewReader(pool, gate, db)
	activityReader := collector.NewActivityReader(pool, gate, db)
	connectionsReader := collector.NewConnectionsReader(pool, gate, db)
	aggregator := collector.NewActivityAggregator(cfg.serverID, cfg.activityFlush)
	shipper := collector.NewShipper(cfg.ingestURL, cfg.token)

	// Optional log-tail pipeline. Constructed only when a log path is set;
	// when unset, logPipe stays nil and the log ticker is never armed, leaving
	// the collector byte-for-byte identical to its log-free behavior.
	var logPipe *collector.LogPipeline
	if cfg.logSourcePath != "" {
		format := logparse.FormatStderr
		if cfg.logSourceFormat == "csv" {
			format = logparse.FormatCSV
		}
		tail := collector.NewFileTail(cfg.logSourcePath)
		defer func() { _ = tail.Close() }()
		logPipe = collector.NewLogPipeline(tail, cfg.serverID, logparse.Options{
			Format:       format,
			StderrPrefix: cfg.logStderrPrefix,
		}, cfg.detectLocally)
	}

	// refreshPolicy fetches effective capability policy from the api server
	// and atomically swaps it into the gate. The collector has no config-DB
	// handle (spec §4.4.0), so policy reaches it only via this HTTP fetch.
	// A failure logs and leaves the previous snapshot in place — fail-open
	// means an unreachable api keeps collecting, never goes silently dark.
	refreshPolicy := func() {
		if cfg.apiBaseURL == "" {
			return // not configured: gate stays empty => all-enabled
		}
		snap, err := collector.FetchPolicySnapshot(ctx, cfg.apiBaseURL, cfg.serverID, db)
		if err != nil {
			log.Printf("refresh policy snapshot: %v", err)
			return
		}
		gate.Replace(snap)
		log.Printf("refreshed policy snapshot: %d entries", len(snap))
	}

	// Single schema-name boundary filter, shared by all catalog readers.
	// Fail fast on a bad regex — a non-compiling pattern must NOT silently
	// disable filtering (that would leak sensitive schema names).
	filter, err := collector.NewSchemaFilter(cfg.includeSchemaRegexp, cfg.ignoreSchemaRegexp)
	if err != nil {
		log.Fatalf("schema filter: %v", err)
	}
	inventory := collector.NewInventory(pool, filter, gate, db)
	tableStatsReader := collector.NewTableStatsReader(pool, filter, gate, db)
	freezeReader := collector.NewFreezeAgeReader(pool, filter, gate, db)

	// Existing path: full snapshot (query stats + schema inventory + table stats) every
	// cfg.interval (~10m). The collector is outbound-only: the inventory
	// ships first-seen-less; the ingestion server resolves + persists
	// first-seen against the stats DB.
	runFull := func() {
		stats, err := reader.Read(ctx)
		if err != nil {
			log.Printf("read query stats: %v", err)
			return
		}
		objs, err := inventory.Read(ctx)
		if err != nil {
			// Non-fatal: ship the query stats we have.
			log.Printf("read schema inventory: %v", err)
			objs = nil
		}
		tableStats, err := tableStatsReader.Read(ctx, cfg.serverID)
		if err != nil {
			log.Printf("collector: table stats read: %v", err)
		}
		freezeAges, err := freezeReader.Read(ctx, cfg.serverID)
		if err != nil {
			log.Printf("collector: freeze age read: %v", err)
		}
		snap := &lynceusv1.Snapshot{
			ServerId:        cfg.serverID,
			CollectedAtUnix: time.Now().Unix(),
			QueryStats:      stats,
			SchemaObjects:   objs,
			TableStats:      tableStats,
			FreezeAges:      freezeAges,
		}
		if err := shipper.Send(ctx, snap); err != nil {
			log.Printf("ship full: %v", err)
			return
		}
		log.Printf("shipped %d query_stats, %d schema_objects, %d table_stats, %d freeze_ages", len(stats), len(objs), len(tableStats), len(freezeAges))
	}

	// Sample pg_stat_activity into the aggregator on the activity cadence.
	sampleActivity := func() {
		samples, err := activityReader.Read(ctx)
		if err != nil {
			log.Printf("read pg_stat_activity: %v", err)
			return
		}
		aggregator.Observe(time.Now().UTC(), samples)
	}

	// Flush aggregator → Snapshot on the bucket cadence.
	flushActivity := func() {
		buckets := aggregator.Flush()
		if len(buckets) == 0 {
			return
		}
		protoBuckets := make([]*lynceusv1.ActivityBucket, 0, len(buckets))
		for i := range buckets {
			b := &buckets[i]
			protoBuckets = append(protoBuckets, &lynceusv1.ActivityBucket{
				ServerId:        b.ServerID,
				DatabaseName:    b.Database,
				State:           b.State,
				WaitEventType:   b.WaitEventType,
				WaitEvent:       b.WaitEvent,
				BucketStartUnix: b.BucketStart.Unix(),
				BucketSeconds:   b.BucketSeconds,
				SampleCount:     b.SampleCount,
				CountSum:        b.CountSum,
				CountMax:        b.CountMax,
			})
		}
		snap := &lynceusv1.Snapshot{
			ServerId:        cfg.serverID,
			CollectedAtUnix: time.Now().Unix(),
			ActivityBuckets: protoBuckets,
		}
		if err := shipper.Send(ctx, snap); err != nil {
			log.Printf("ship activity: %v", err)
			return
		}
		log.Printf("shipped %d activity_buckets", len(protoBuckets))
	}

	// Sample per-backend connection durations + blocking edges point-in-time
	// and ship them on the same 60s flush cadence (matches the checks
	// scheduler tick). T1: pids + durations + fixed state labels only.
	shipConnections := func() {
		samples, edges, err := connectionsReader.Read(ctx)
		if err != nil {
			log.Printf("read connections: %v", err)
			return
		}
		if len(samples) == 0 && len(edges) == 0 {
			return
		}
		nowUnix := time.Now().Unix()
		protoSamples := make([]*lynceusv1.ConnectionSample, 0, len(samples))
		for i := range samples {
			s := &samples[i]
			protoSamples = append(protoSamples, &lynceusv1.ConnectionSample{
				ServerId:       cfg.serverID,
				ObservedAtUnix: nowUnix,
				Pid:            s.PID,
				State:          s.State,
				ActiveSeconds:  s.ActiveSeconds,
				XactSeconds:    s.XactSeconds,
				StateSeconds:   s.StateSeconds,
				WaitEventType:  s.WaitEventType,
			})
		}
		protoEdges := make([]*lynceusv1.BlockingEdge, 0, len(edges))
		for i := range edges {
			e := &edges[i]
			protoEdges = append(protoEdges, &lynceusv1.BlockingEdge{
				ServerId:           cfg.serverID,
				ObservedAtUnix:     nowUnix,
				BlockedPid:         e.BlockedPID,
				BlockerPid:         e.BlockerPID,
				BlockedWaitSeconds: e.BlockedWaitSeconds,
			})
		}
		snap := &lynceusv1.Snapshot{
			ServerId:          cfg.serverID,
			CollectedAtUnix:   nowUnix,
			ConnectionSamples: protoSamples,
			BlockingEdges:     protoEdges,
		}
		if err := shipper.Send(ctx, snap); err != nil {
			log.Printf("ship connections: %v", err)
			return
		}
		log.Printf("shipped %d connection_samples, %d blocking_edges", len(protoSamples), len(protoEdges))
	}

	// Drain the log tail → ship classified T1 log events + extracted plans.
	runLogTail := func() {
		res, err := logPipe.Drain()
		if err != nil {
			log.Printf("drain log tail: %v", err)
			return
		}
		if len(res.LogEvents) == 0 && len(res.QueryPlans) == 0 {
			return
		}
		snap := &lynceusv1.Snapshot{
			ServerId:        cfg.serverID,
			CollectedAtUnix: time.Now().Unix(),
			LogEvents:       res.LogEvents,
			QueryPlans:      res.QueryPlans,
		}
		if err := shipper.Send(ctx, snap); err != nil {
			log.Printf("ship log: %v", err)
			return
		}
		log.Printf("shipped %d log_events, %d query_plans (%d local insights)",
			len(res.LogEvents), len(res.QueryPlans), len(res.Insights))
	}

	// Kick off one of each immediately. Refresh policy first so the very
	// first full snapshot already respects capability policy.
	refreshPolicy()
	runFull()
	sampleActivity()

	fullTicker := time.NewTicker(cfg.interval)
	defer fullTicker.Stop()
	sampleTicker := time.NewTicker(cfg.activityInterval)
	defer sampleTicker.Stop()
	flushTicker := time.NewTicker(cfg.activityFlush)
	defer flushTicker.Stop()

	// A nil channel blocks forever in select, so when no log source is
	// configured this arm is inert and the loop behaves exactly as before.
	var logTickerC <-chan time.Time
	if logPipe != nil {
		logTicker := time.NewTicker(cfg.logTailInterval)
		defer logTicker.Stop()
		logTickerC = logTicker.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-fullTicker.C:
			refreshPolicy()
			runFull()
		case <-sampleTicker.C:
			sampleActivity()
		case <-flushTicker.C:
			flushActivity()
			shipConnections()
		case <-logTickerC:
			runLogTail()
		}
	}
}

type config struct {
	serverID            string
	pgDSN               string
	ingestURL           string
	token               string
	apiBaseURL          string        // LYNCEUS_API_BASE_URL; "" disables policy fetch (gate stays all-enabled)
	includeSchemaRegexp string        // LYNCEUS_INCLUDE_SCHEMA_REGEXP ("" = allow any)
	ignoreSchemaRegexp  string        // LYNCEUS_IGNORE_SCHEMA_REGEXP ("" = exclude none)
	interval            time.Duration // full snapshot cadence
	activityInterval    time.Duration // pg_stat_activity sample cadence (~10s)
	activityFlush       time.Duration // bucket flush cadence (60s)

	logSourcePath   string        // LYNCEUS_LOG_SOURCE_PATH; "" disables log ingestion
	logSourceFormat string        // LYNCEUS_LOG_FORMAT: "csv" | "stderr" (default stderr)
	logStderrPrefix string        // LYNCEUS_LOG_STDERR_PREFIX; default "%m [%p] "
	logTailInterval time.Duration // LYNCEUS_LOG_TAIL_INTERVAL; default 2s
	detectLocally   bool          // LYNCEUS_INSIGHT_LOCAL == "1"
}

func loadConfig() config {
	c := config{
		serverID:            os.Getenv("LYNCEUS_SERVER_ID"),
		pgDSN:               os.Getenv("LYNCEUS_PG_DSN"),
		ingestURL:           os.Getenv("LYNCEUS_INGESTION_URL"),
		token:               os.Getenv("LYNCEUS_COLLECTOR_TOKEN"),
		apiBaseURL:          os.Getenv("LYNCEUS_API_BASE_URL"),
		includeSchemaRegexp: os.Getenv("LYNCEUS_INCLUDE_SCHEMA_REGEXP"),
		ignoreSchemaRegexp:  os.Getenv("LYNCEUS_IGNORE_SCHEMA_REGEXP"),
		interval:            10 * time.Minute,
		activityInterval:    10 * time.Second,
		activityFlush:       60 * time.Second,
		logSourcePath:       os.Getenv("LYNCEUS_LOG_SOURCE_PATH"),
		logSourceFormat:     os.Getenv("LYNCEUS_LOG_FORMAT"),
		logStderrPrefix:     os.Getenv("LYNCEUS_LOG_STDERR_PREFIX"),
		logTailInterval:     2 * time.Second,
		detectLocally:       os.Getenv("LYNCEUS_INSIGHT_LOCAL") == "1",
	}
	if v := os.Getenv("LYNCEUS_COLLECTOR_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.interval = d
		}
	}
	if v := os.Getenv("LYNCEUS_ACTIVITY_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.activityInterval = d
		}
	}
	if v := os.Getenv("LYNCEUS_ACTIVITY_FLUSH"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.activityFlush = d
		}
	}
	if v := os.Getenv("LYNCEUS_LOG_TAIL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.logTailInterval = d
		}
	}
	if c.serverID == "" || c.pgDSN == "" || c.ingestURL == "" {
		log.Fatal("LYNCEUS_SERVER_ID, LYNCEUS_PG_DSN, and LYNCEUS_INGESTION_URL are required")
	}
	return c
}
