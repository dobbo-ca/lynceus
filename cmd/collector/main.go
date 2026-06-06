package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/collector"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

func main() {
	cfg := loadConfig()

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.pgDSN)
	if err != nil {
		log.Fatalf("connect monitored postgres: %v", err)
	}
	defer pool.Close()

	reader := collector.NewReader(pool)
	activityReader := collector.NewActivityReader(pool)
	aggregator := collector.NewActivityAggregator(cfg.serverID, cfg.activityFlush)
	shipper := collector.NewShipper(cfg.ingestURL, cfg.token)

	// Existing path: full snapshot (query stats) every cfg.interval (~10m).
	runFull := func() {
		stats, err := reader.Read(ctx)
		if err != nil {
			log.Printf("read query stats: %v", err)
			return
		}
		snap := &lynceusv1.Snapshot{
			ServerId:        cfg.serverID,
			CollectedAtUnix: time.Now().Unix(),
			QueryStats:      stats,
		}
		if err := shipper.Send(ctx, snap); err != nil {
			log.Printf("ship full: %v", err)
			return
		}
		log.Printf("shipped %d query_stats", len(stats))
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
		for _, b := range buckets {
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

	// Kick off one of each immediately.
	runFull()
	sampleActivity()

	fullTicker := time.NewTicker(cfg.interval)
	defer fullTicker.Stop()
	sampleTicker := time.NewTicker(cfg.activityInterval)
	defer sampleTicker.Stop()
	flushTicker := time.NewTicker(cfg.activityFlush)
	defer flushTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-fullTicker.C:
			runFull()
		case <-sampleTicker.C:
			sampleActivity()
		case <-flushTicker.C:
			flushActivity()
		}
	}
}

type config struct {
	serverID         string
	pgDSN            string
	ingestURL        string
	token            string
	interval         time.Duration // full snapshot cadence
	activityInterval time.Duration // pg_stat_activity sample cadence (~10s)
	activityFlush    time.Duration // bucket flush cadence (60s)
}

func loadConfig() config {
	c := config{
		serverID:         os.Getenv("LYNCEUS_SERVER_ID"),
		pgDSN:            os.Getenv("LYNCEUS_PG_DSN"),
		ingestURL:        os.Getenv("LYNCEUS_INGESTION_URL"),
		token:            os.Getenv("LYNCEUS_COLLECTOR_TOKEN"),
		interval:         10 * time.Minute,
		activityInterval: 10 * time.Second,
		activityFlush:    60 * time.Second,
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
	if c.serverID == "" || c.pgDSN == "" || c.ingestURL == "" {
		log.Fatal("LYNCEUS_SERVER_ID, LYNCEUS_PG_DSN, and LYNCEUS_INGESTION_URL are required")
	}
	return c
}
