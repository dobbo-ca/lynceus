# M3 Checks Bundle — System (Out of Disk Space) Design

**Bead:** ly-u4t.25 (P1). **Parity:** System checks. **Depends on:** ly-u4t.20 (Checks engine, merged).

## Problem

"Out of Disk Space" is the one M3 check whose signal does **not** live inside Postgres. A
least-privileged, read-only Postgres role cannot see host free disk space via SQL — `pg_database_size()`
/ `pg_tablespace_size()` / `sum(relation sizes)` report what *Postgres logically uses*, not the
filesystem's true capacity or free bytes, and they miss WAL, system catalogs, bloat in shared
structures, and any other database sharing the volume. Comparing them against a guessed capacity would
produce dangerous false-negatives for a check whose entire purpose is to warn before a volume fills.

The honest signal is **infrastructure storage metrics** from outside Postgres:

- **Self-managed / bare-metal:** the host filesystem (`statfs`) — but only when the collector can see
  that filesystem. With the collector deployed as a k8s sidecar or on a separate node, this is the
  **unlikely** case.
- **Managed (the common case):** the cloud provider's metrics API — AWS RDS CloudWatch
  `FreeStorageSpace`, Azure Database for PostgreSQL Flexible Server via Azure Monitor.

A single logical database is also served by **multiple instances** — a writer plus dedicated read
replicas, each with its own independent volume (a replica can fill its disk from retained WAL while the
writer is fine). Connection poolers (pgbouncer) do not hold the data volume and are out of scope for
this check.

## Decision

Introduce a **new, source-agnostic telemetry type** — `DiskUsage` — modeled per *instance* and tagged
with its *source*, so host / AWS / Azure metrics all flow through one wire message, one store table, and
one check with **no schema change** as sources are added. The codebase has no infrastructure-metrics
scaffolding today; this bead lays that foundation.

This bead (ly-u4t.25) ships:

1. The `DiskUsage` T1 data model (proto → store → check), per-instance + source-tagged.
2. Exactly **one concrete source**: the **host `statfs`** reader. It is the only source implementable
   without a cloud SDK or credential model, it is testable in CI with no cloud mocks, and it covers the
   self-managed case. It proves the pipeline end-to-end.
3. The `system.disk_space` check evaluating every `DiskUsage` row.

Follow-up beads (filed as part of this work):

- **disk: AWS RDS CloudWatch source** — production-primary; enumerates cluster instances, ships one
  `DiskUsage` row per writer/reader from `FreeStorageSpace` + allocated storage. Needs the AWS SDK + an
  IAM/IRSA credential model.
- **disk: Azure Monitor source** — Azure Flexible Server analog.
- **disk: multi-instance (writer + readers) collector topology** — how one collector (or a fleet)
  enumerates and attributes cluster members.
- (optional) **disk: server-side tunable warn/critical thresholds.**

### Why host source measures capacity directly

With real host metrics, `total_bytes` is **measured** from `statfs` (`f_blocks`), so there is no
operator-configured capacity. The operator configures only *which paths* to measure. (Cloud sources
likewise read allocated storage from the provider API.) This removes the `LYNCEUS_DISK_CAPACITY_BYTES`
config the SQL-only approach would have required.

## Data model — `DiskUsage` T1 message

`Snapshot.disk_usage = 13` (repeated `DiskUsage`). Every field is an identifier, a fixed-vocabulary
label, or a byte count — no literal-bearing field. Same privacy class as `FreezeAge`/`TableStat`.

| field | type | source | used by |
|---|---|---|---|
| `source` | string | `"host"` \| `"aws_rds"` \| `"azure_flexible"` | provenance; fixed-vocab label (matches `scope`/`state` string convention) |
| `instance` | string | server_id (host); cluster member id (cloud, later) | per-instance identity — writer + readers |
| `role` | string | `pg_is_in_recovery()` → `"primary"`/`"replica"` (host); cloud API (later) | a replica's disk fills independently; surfaced in Detail |
| `mount` | string | operator path (host); volume/metric id (cloud) | which filesystem; operator-authored, structural |
| `total_bytes` | int64 | statfs `f_blocks*f_bsize` | capacity (measured) |
| `used_bytes` | int64 | statfs `(f_blocks-f_bfree)*f_bsize` | numerator of %-used |
| `available_bytes` | int64 | statfs `f_bavail*f_bsize` | the "out of disk" headroom |

`mount` is operator-authored infrastructure metadata (a config path or a cloud volume id), never a
value drawn from the monitored database — T1-safe, like a `datname`. `role` is real *now* via a cheap
read-only `SELECT pg_is_in_recovery()`; it can change on failover, so it is a value, not a key.

Store key (latest-per): `(server_id, source, instance, mount)`. `role` is a value column.

## Components

### `internal/collector` — host source

`DiskUsageReader` (gated by new `caps.DiskMetrics`):

- Config: `LYNCEUS_DISK_PATHS` — comma-separated mount points (PGDATA volume, optionally a separate WAL
  volume). **Empty ⇒ reader ships nothing ⇒ check stays silent** — the graceful "managed targets skip"
  behavior until their cloud follow-up bead lands.
- For each path: `statfs` → `total/used/available`. Plus one `SELECT pg_is_in_recovery()` → `role`.
- Portability mirrors the existing `logsource_unix.go` / `logsource_other.go` precedent:
  `disk_statfs_unix.go` (`//go:build unix`, `syscall.Statfs`) + `disk_statfs_other.go`
  (`//go:build !unix`, returns "unsupported on this OS"). No third-party dependency.
- Runs on the existing full (~10m) snapshot cadence beside the catalog readers.
- **Deployment caveat (documented):** the host source is meaningful only when the collector shares the
  monitored instance's filesystem (bare-metal, or a sidecar that mounts the data volume). In managed /
  separate-node deployments it reports nothing and the cloud sources are used instead.

### `internal/store` — `disk_usage` table

Migration `0013_disk_usage.sql`: weekly range-partitioned (vanilla Postgres, RDS-safe), columns
`server_id, collected_at, source, instance, role, mount, total_bytes, used_bytes, available_bytes,
data_tier`. `WriteDiskUsage` (COPY + partition ensure) and `LatestDiskUsage` (latest per
`(source, instance, mount)`), mirroring `freeze_ages.go`.

### `internal/checks` — `system.disk_space`

Pure, scheduler-side, registered via `init()`+`Register()`. Per `DiskUsage` row:

- skip if `total_bytes <= 0` (no measurement);
- `pct = used_bytes / total_bytes`; `pctWarn = 0.85`, `pctCritical = 0.95` (constants — tunable
  thresholds are a follow-up);
- `Object = instance + ":" + mount`; `Detail` names role, used/total bytes, the percentage, and
  available bytes.

Fires once per instance×mount, so it covers the writer and every replica automatically once cloud /
topology beads ship their rows. `Input.Disk []DiskInfo` carries the projection; the scheduler assembles
it from `LatestDiskUsage`.

### `internal/caps` — capability

`DiskMetrics Capability = "disk_metrics"` + a `Declared()` entry. No probe (gate is policy-driven,
default fail-open), mirroring `IndexStats` / `FreezeAge`.

## Data flow

```
collector: statfs(paths) + pg_is_in_recovery()  ──► []DiskUsage  (gated by caps.DiskMetrics)
   └─ Snapshot.disk_usage ──► ingest.persistSnapshot ──► store.WriteDiskUsage (disk_usage table)
                                                              │
scheduler.assembleInput ◄── store.LatestDiskUsage ◄──────────┘
   └─ Input.Disk ──► checks.DiskSpaceCheck.Eval ──► []Result ──► checks_results (+ notify)
```

## Invariants

- **T1 only** — `DiskUsage` carries identifiers, fixed-vocab labels, and byte counts; a contract test
  allowlists exactly those fields and asserts `Snapshot.disk_usage = 13`.
- **Postgres read-only** — the only SQL is `SELECT pg_is_in_recovery()`; `statfs` is a read syscall.
- **Collector outbound-only** — host source adds no inbound surface; cloud sources (later) make
  outbound API calls only.
- **No dependency on TimescaleDB** — `disk_usage` is a vanilla partitioned table.

## Testing (TDD, testcontainers + `internal/testpg.ReadyWait()`)

- **contract:** `DiskUsage` field allowlist + scalar shapes; `Snapshot.disk_usage = 13`; Snapshot
  envelope allowlist grows by one.
- **store:** `WriteDiskUsage` → `LatestDiskUsage` roundtrip; partition created.
- **collector:** reader integration — `statfs` a `t.TempDir()` + `pg_is_in_recovery()` on a testcontainer
  (asserts `total>0`, `available>=0`, `role="primary"`, `source="host"`, `mount`=tmp path); gated-off
  returns nil; empty `LYNCEUS_DISK_PATHS` returns nil.
- **ingest:** focused snapshot round-trip persists one `disk_usage` row.
- **checks:** severity-ladder unit (below-warn → none; warn band → warning; critical band → critical;
  `total=0` → skipped) + registration test.
- **scheduler:** assembly populates `Input.Disk` from `LatestDiskUsage`.

## Out of scope (this bead)

AWS/Azure source implementations; cloud credential model; multi-instance cluster enumeration; pooler
(pgbouncer) disk; server-side tunable thresholds; alert routing changes. All tracked as follow-up beads.
