// Package testpg centralizes the Postgres testcontainer readiness wait used
// by the integration tests.
//
// It replaces the postgres module's BasicWaitStrategies() (and ad-hoc
// port-only waits) with a wait that actually confirms the server accepts
// connections: wait for the listening port, then run pg_isready over TCP
// inside the container. This avoids two CI-Docker failure modes seen with the
// log-line / port-only strategies:
//
//   - "connection reset by peer" on the first query, because the mapped port
//     opens (and the "ready" log can appear) during the postgres entrypoint's
//     temporary init server, before the final server accepts TCP connections;
//   - log-line waits being unusable when logging_collector=on redirects the
//     "database system is ready" message away from stderr.
//
// pg_isready -h 127.0.0.1 only succeeds against the final TCP server, so it is
// robust regardless of logging configuration.
package testpg

import (
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ReadyWait is a drop-in replacement for tcpostgres.BasicWaitStrategies():
// it waits for the listening port and then for pg_isready (over TCP) to
// report the server is accepting connections.
func ReadyWait() testcontainers.CustomizeRequestOption {
	return testcontainers.WithWaitStrategy(
		wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second),
		wait.ForExec([]string{"pg_isready", "-h", "127.0.0.1", "-p", "5432"}).
			WithStartupTimeout(60*time.Second),
	)
}
