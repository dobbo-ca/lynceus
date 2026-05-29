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
	shipper := collector.NewShipper(cfg.ingestURL, cfg.token)

	runOnce := func() {
		stats, err := reader.Read(ctx)
		if err != nil {
			log.Printf("read: %v", err)
			return
		}
		snap := &lynceusv1.Snapshot{
			ServerId:        cfg.serverID,
			CollectedAtUnix: time.Now().Unix(),
			QueryStats:      stats,
		}
		if err := shipper.Send(ctx, snap); err != nil {
			log.Printf("ship: %v", err)
			return
		}
		log.Printf("shipped %d query_stats", len(stats))
	}

	runOnce()
	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

type config struct {
	serverID  string
	pgDSN     string
	ingestURL string
	token     string
	interval  time.Duration
}

func loadConfig() config {
	c := config{
		serverID:  os.Getenv("LYNCEUS_SERVER_ID"),
		pgDSN:     os.Getenv("LYNCEUS_PG_DSN"),
		ingestURL: os.Getenv("LYNCEUS_INGESTION_URL"),
		token:     os.Getenv("LYNCEUS_COLLECTOR_TOKEN"),
		interval:  10 * time.Minute,
	}
	if v := os.Getenv("LYNCEUS_COLLECTOR_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.interval = d
		}
	}
	if c.serverID == "" || c.pgDSN == "" || c.ingestURL == "" {
		log.Fatal("LYNCEUS_SERVER_ID, LYNCEUS_PG_DSN, and LYNCEUS_INGESTION_URL are required")
	}
	return c
}
