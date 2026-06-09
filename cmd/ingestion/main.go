package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/checks"
	"github.com/dobbo-ca/lynceus/internal/ingest"
	"github.com/dobbo-ca/lynceus/internal/secure"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func main() {
	dsn := os.Getenv("LYNCEUS_STATS_DSN")
	if dsn == "" {
		log.Fatal("LYNCEUS_STATS_DSN required")
	}
	if err := secure.CheckDatabaseDSN(dsn, secure.RequireTLS()); err != nil {
		log.Fatal(err)
	}
	addr := envDefault("LYNCEUS_INGESTION_ADDR", ":8081")
	token := os.Getenv("LYNCEUS_DEV_TOKEN") // empty disables auth — dev only

	rateLimit := envFloat("LYNCEUS_RATE_LIMIT", 5.0)
	rateBurst := envInt("LYNCEUS_RATE_BURST", 10)

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect stats db: %v", err) //nolint:gocritic // exitAfterDefer: deferred cleanup is best-effort on a fatal process exit
	}
	defer pool.Close()

	if err := store.ApplyStatsMigrations(ctx, pool); err != nil {
		log.Fatalf("migrate stats: %v", err)
	}

	srv := ingest.NewServer(ingest.Config{
		DevToken: token, RateLimit: rateLimit, RateBurst: rateBurst,
	}, store.NewStats(pool), pool)

	checksInterval := time.Duration(envInt("LYNCEUS_CHECKS_INTERVAL_SEC", 60)) * time.Second
	scheduler := checks.NewScheduler(store.NewStats(pool), checks.DefaultChecks(), checks.NopNotifier{}).
		WithInterval(checksInterval)
	go scheduler.Run(ctx)

	httpSrv := &http.Server{
		Addr:        addr,
		Handler:     srv.Handler(),
		ReadTimeout: 60 * time.Second,
	}
	go func() {
		log.Printf("lynceus ingestion listening on %s", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func envDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envFloat(k string, d float64) float64 {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return d
	}
	return f
}
func envInt(k string, d int) int {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return d
	}
	return i
}
