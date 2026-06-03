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
	"github.com/dobbo-ca/lynceus/internal/store"
)

func main() {
	dsn := os.Getenv("LYNCEUS_STATS_DSN")
	if dsn == "" {
		log.Fatal("LYNCEUS_STATS_DSN required")
	}
	configDSN := os.Getenv("LYNCEUS_CONFIG_DSN")
	if configDSN == "" {
		log.Fatal("LYNCEUS_CONFIG_DSN required")
	}
	addr := os.Getenv("LYNCEUS_API_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	devAuth := os.Getenv("LYNCEUS_DEV_AUTH") == "true"

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect stats db: %v", err)
	}
	defer pool.Close()

	configPool, err := pgxpool.New(ctx, configDSN)
	if err != nil {
		log.Fatalf("connect config db: %v", err)
	}
	defer configPool.Close()

	srv := api.NewServer(api.Config{DevAuth: devAuth},
		store.NewStats(pool), store.NewConfig(configPool))

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
