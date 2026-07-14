package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dobbo-ca/lynceus/internal/api"
	"github.com/dobbo-ca/lynceus/internal/secure"
	"github.com/dobbo-ca/lynceus/internal/store"
)

func main() {
	configDSN := os.Getenv("LYNCEUS_CONFIG_DSN")
	if configDSN == "" {
		log.Fatal("LYNCEUS_CONFIG_DSN required")
	}
	configRODSN := os.Getenv("LYNCEUS_CONFIG_RO_DSN")
	// Stats DSNs are validated inside store.OpenStats (per backend); the
	// config DB is always vanilla Postgres.
	for _, d := range []string{configDSN, configRODSN} {
		if d == "" {
			continue
		}
		if err := secure.CheckDatabaseDSN(d, secure.RequireTLS()); err != nil {
			log.Fatal(err)
		}
	}
	addr := os.Getenv("LYNCEUS_API_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	devAuth := os.Getenv("LYNCEUS_DEV_AUTH") == "true"
	enableOpensearch := os.Getenv("LYNCEUS_ENABLE_OPENSEARCH") == "true"
	enableElasticsearch := os.Getenv("LYNCEUS_ENABLE_ELASTICSEARCH") == "true"
	enableRedis := os.Getenv("LYNCEUS_ENABLE_REDIS") == "true"
	enableValkey := os.Getenv("LYNCEUS_ENABLE_VALKEY") == "true"

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Stats backend selected by LYNCEUS_STATS_BACKEND (postgres|clickhouse);
	// OpenStats opens + migrates it and, for postgres, attaches the optional
	// LYNCEUS_STATS_RO_DSN read replica.
	stats, err := store.OpenStats(ctx)
	if err != nil {
		log.Fatalf("open stats backend: %v", err) //nolint:gocritic // exitAfterDefer: deferred cleanup is best-effort on a fatal process exit
	}

	configPool, err := pgxpool.New(ctx, configDSN)
	if err != nil {
		log.Fatalf("connect config db: %v", err)
	}
	defer configPool.Close()

	configRO := openReadPool(ctx, configRODSN, "config")
	defer closePool(configRO)

	srv := api.NewServer(api.Config{
		DevAuth:             devAuth,
		EnableOpensearch:    enableOpensearch,
		EnableElasticsearch: enableElasticsearch,
		EnableRedis:         enableRedis,
		EnableValkey:        enableValkey,
	},
		stats,
		store.NewConfig(configPool).WithReadPool(configRO))

	httpSrv := &http.Server{
		Addr:        addr,
		Handler:     srv.Handler(),
		ReadTimeout: 30 * time.Second,
	}
	go func() {
		log.Printf("lynceus api listening on %s (dev-auth=%v)", addr, devAuth)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

// openReadPool opens a read-replica pool when dsn is non-empty; a fatal
// error is raised on a bad DSN so misconfiguration is caught at startup.
// Returns nil when dsn is empty, in which case the store falls back to
// its primary pool.
func openReadPool(ctx context.Context, dsn, name string) *pgxpool.Pool {
	if dsn == "" {
		return nil
	}
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect %s read replica: %v", name, err)
	}
	return p
}

// closePool closes a pool if it is non-nil.
func closePool(p *pgxpool.Pool) {
	if p != nil {
		p.Close()
	}
}
