# Lynceus Parity — Layer 0 Foundation (Spec)

> Date: 2026-06-08 · Part of the dependency-layered parity program (see roadmap). Sub-project 1 of 4 layers.
>
> **Correction (2026-06-08, ly-gu3):** the insight HTTP-surfacing work in this spec was originally tracked as `ly-u4t.21`. That bead is in fact **"M3: Checks bundle — Queries"** (a Layer-2 Checks bundle depending on the `ly-u4t.20` Checks engine, still open). The merged `/insights` surfacing is now tracked by **`ly-hnt`** (closed, PR #19). All references below have been re-attributed `ly-u4t.21` → `ly-hnt`.

## 1. Overview & goal

Layer 0 is the keystone of the parity program: it **turns on engines that already exist but are dormant**, and **lays the catalog data foundation** every higher layer reads from. Today the collector ships `query_stats`, `activity_buckets`, and `query_plans` but never tails a log file (the `logparse`/`planextract`/`insight` packages are built and tested yet unwired — `docs/GOALS.md:81`), exposes no schema/table inventory, surfaces no insights over HTTP, and has no per-capability policy gate. Layer 0 closes exactly those gaps: (a) a rotation-aware file-tail `LogSource` feeds `logparse.ParseStream` so classified T1 `LogEvent`s and `auto_explain`-derived T1 `QueryPlan`s finally flow (ly-cxe.2); (b) two read-only catalog readers produce a structural schema/object inventory plus a per-table size/growth/TOAST time series (ly-xqf.5, ly-xqf.6) — the literal data foundation the Layer 1 Index Advisor (ly-u4t.12) and Layer 2 VACUUM/config/checks engines (ly-u4t.16/.18/.20) depend on; (c) the existing insight engine and stored plans become server-rendered templ/HTMX views (ly-hnt, ly-xqf.10); and (d) a per-server capability matrix API plus a cheap collector-local effective-policy gate ensures no row is built or shipped for a disabled capability (ly-xnk.4, ly-xnk.3). The unifying contract is privacy: **every new wire field is an identifier, count, size, duration, enum, or fingerprint — never a literal** — enforced by the proto-schema contract test and the e2e canary.

## 2. Scope (beads covered)

| Bead | Component | One-line |
|---|---|---|
| ly-cxe.2 | Collector wiring + file-tail log source | Rotation-aware `FileTail` → `logparse.ParseStream` → T1 `LogEvent`s + extracted `QueryPlan`s on a new log ticker. |
| ly-xqf.5 | Schema/object inventory reader | Walk `pg_namespace`/`pg_class`/`pg_index`/`pg_proc` → T1 `SchemaObject` upsert with stable first-seen, schema-regex gated. |
| ly-xqf.6 | Table size/growth + TOAST reader | `pg_class` + `pg_stat_user_tables` → T1 `TableStat` weekly-partitioned growth series (heap/toast/index split, dead-tuple/vacuum metrics). |
| ly-hnt | Insight HTTP surfacing | `/insights` list + detail: load stored plans via `TopPlansByQuery`, run `insight.DetectPlans`, render with Severity. |
| ly-xqf.10 | Plan visualization | `/plan?server=&fp=`: recursive `PlanNode` tree + flat node grid. |
| ly-xnk.4 | Capability matrix API | `GET` discovered × policy × final-enabled matrix; audited `POST` toggle reusing `SetCapabilityPolicy`. |
| ly-xnk.3 | Effective-policy reader gate | Collector-local in-memory `caps.Gate` consulted before every reader query; retrofit onto stat_statements/activity readers, born into the new schema/table readers. |

**Explicitly OUT of scope for Layer 0:**
- **Layer 1 detectors / advisors** (Index Advisor ly-u4t.12, etc.) — they *consume* the schema/table foundation but are not built here. Pull nothing from Layer 1.
- **Workstream B (HA/perf):** flush-on-shutdown of in-memory buckets and the final unshipped log chunk (`cmd/collector/main.go:110-111`) — same class of gap, intentionally untouched. The only shutdown action ly-cxe.2 adds is `defer tail.Close()`.
- **T2 raw payloads** (raw log line, raw plan body) — remain collector-local, never written by any Layer 0 store writer.
- **Retention generalization:** `DropPartitionsOlderThan` (`stats.go` hardcoded to `query_stats`) is not generalized here; tracked as follow-up so `table_stats`/log partitions don't grow unbounded.
- **Real OIDC actor wiring** — audited toggles use the `dev-admin` stub under `DevAuth` (Milestone-5 follow-up).

## 3. Privacy & proto-contract guardrails (READ FIRST)

This is the gate **every** proto/store change below must pass. The privacy guarantee is enforced by the proto **schema itself**, not runtime checks (`docs/specs/2026-05-29-lynceus-design.md:41-44`).

### 3.1 The rule — allowed field shapes on any T1 message

Every field on every new T1 message MUST be one of these shapes ONLY:

- **identifier** — a Postgres catalog name (table/index/schema/database/role/application/column name). Low-risk normalized metadata, e.g. `PlanNode.relation_name` (`proto/lynceus/v1/plan.proto:42`), `LogEvent.database_name` "catalog identifier, not user data" (`log_event.proto:50-51`).
- **fingerprint / hash** — stable hash of normalized structure, e.g. `QueryStat.fingerprint`, or a SHA-256 standing in for PII, e.g. `LogEvent.client_addr_hash` (raw IP is PII under GDPR — `log_event.proto:60-62`).
- **count** — `int64`/`int32` aggregate, e.g. `PlanNode.rows_removed_by_filter` "A COUNT, never a literal" (`plan.proto:65-66`).
- **size** — byte/width counter, e.g. `PlanNode.plan_width`.
- **duration / metric** — `double` ms or cost, e.g. `total_time_ms`, `total_cost`, `actual_total_time_ms`.
- **enum / fixed-vocabulary string** — value from a closed Lynceus/Postgres vocabulary, e.g. `LogEvent.event_type` (closed list `log_event.proto:24-32`), `severity`, 5-char `sql_state`, `ActivityBucket.state`.
- **normalized string** — literals already replaced by `$n`, DROPPED to empty if literal-freedom cannot be proven: `QueryStat.normalized_query` (`snapshot.proto`), `PlanNode.normalized_condition` "Empty if the source condition could not be proven literal-free — fail closed" (`plan.proto:58-61`).

**FORBIDDEN on any new T1 message** (each WOULD carry a per-execution literal): `raw_text`, `message`, `statement`, `statement_text`, `query_sample`, `parameter_value`, `bind_param`, `error_detail`, `detail`, `hint`, `output`, `query_text`, un-normalized `filter`, `mcv_values`, `histogram_bounds`, `client_addr` (unhashed). These are T2 only — a separate message type, RBAC+audit-gated, disabled by default per server (`design.md:42`, `snapshot.proto:9-12`).

### 3.2 The contract-test mechanism (the build-time gate)

`internal/proto/lynceus/v1/contract_test.go` (234 lines) enforces this by **field-name allowlist over the proto descriptor**. The reusable helper:

```go
func assertOnlyAllowed(t *testing.T, fields protoreflect.FieldDescriptors, allowed map[string]struct{}, msgName string) {
    for i := 0; i < fields.Len(); i++ {
        name := string(fields.Get(i).Name())
        if _, ok := allowed[name]; !ok {
            t.Fatalf("unexpected field %q in T1 %s — possible literal leak. ...", name, msgName)
        }
    }
}
```

It walks `(&Msg{}).ProtoReflect().Descriptor().Fields()` and `t.Fatalf`s on ANY field name not in a hand-maintained `allowed` set (pattern: `contract_test.go:32-44` QueryStat, `:86-99` LogEvent, `:108-136` ActivityBucket, `:182-200` QueryPlan/PlanNode). A second guard asserts field *kind*: scalar-string fields must stay `string`, never `bytes` or a nested message (`contract_test.go:51-60`, `:141-152`, `:205-214`, `:219-234`). **Adding any field is a test-red event until a human deliberately edits the allowlist.** The allowlist edit MUST land in the **same commit** as the proto change or CI fails by design.

**Gate rule for every component below:** any new T1 message OR new field on an existing T1 message MUST (a) be added to a per-message `allowed` set via `assertOnlyAllowed`, and (b) if string-typed, be added to a `wantString` kind-check. No allowlist test = the proto_change is rejected at review.

### 3.3 Per-message merge checklist

Before any Layer 0 proto_change merges, the producing reader/surface must satisfy ALL of:
1. Every field maps to one of the six allowed shapes in §3.1. No field whose value originates from row contents, predicate literals, MCV values, or raw text.
2. A `TestXxxHasOnly…Fields` allowlist test exists for the new message via `assertOnlyAllowed`, plus a string-kind guard per string field.
3. **Log events:** classified event TYPE only — `LogEvent.event_type` is a closed vocabulary (`log_event.proto:24-32`); no `message`/`statement`/`detail`/`hint`/`parameter` field. Raw line + bind params + error detail belong in collector-local T2 `LogPayload`.
4. **Schema/table object NAMES are identifiers, classified T1.** Catalog identifiers travel as T1 metadata (`design.md:41`); the redaction knob is the collector-side `ignore_schema_regexp` filter, NOT promotion to T2 (`features.md:108`).
5. **Column statistics: scalars only.** `null_frac`/`n_distinct`/avg-width allowed; MCV values and histogram bounds FORBIDDEN (`features.md:112`). (No column-stats message ships in Layer 0 — guardrail for the future reader.)
6. **Plan surfacing: structural metadata + normalized conditions only.** No `Output`, `Query Text`, or un-normalized `Filter`. Store has NO raw-plan-text column (mirror `0004_query_plans.sql:5-6`).
7. Any free-text-derived field routes through the fail-closed normalize gateway (`internal/normalize/normalize.go` — `Normalize` returns `("", TierBlocked)` on parse failure) or is dropped.

### 3.4 data_tier classification (all four new Layer 0 wire types are T1)

`data_tier` is `SMALLINT NOT NULL DEFAULT 1` on every stats table (1=T1, 2=T2); writers default `DataTier==0 → 1` (`plans.go:51-53`).

| Layer 0 data type | Tier | Why |
|---|---|---|
| Schema inventory (names, first-seen, sizes) | **T1** | Names are catalog identifiers = "schema metadata" (`design.md:41`); sizes are counts. Redaction = `ignore_schema_regexp` filter, not tiering. |
| Table statistics (size/growth, TOAST, dead rows) | **T1** | All aggregate counts/ratios ("All statistical, no PII", `features.md:73`). |
| Classified log events | **T1** | Only `event_type` + classification + hashed client addr. The raw line / statement / bind params are **T2** `LogPayload`, default-disabled, RBAC+audit-gated — NOT shipped by Layer 0. |
| Plans (normalized auto_explain) | **T1** | Structural tree + normalized conditions only; "no raw-plan-text column" (`0004_query_plans.sql:5-6`). Literal-bearing source plan body stays in T2 `LogPayload`. |

**Store guardrail:** every new stats table includes `data_tier SMALLINT NOT NULL DEFAULT 1` and its COPY writer defaults `DataTier==0→1`. No raw-text columns anywhere.

### 3.5 e2e canary extension (the cross-cutting proof)

`test/e2e/slice_test.go` plants `canaryLiteral = "PHI-CANARY-LEAK-9c2e3a"` (`slice_test.go:36`) in a real query, then asserts it appears NOWHERE at three checkpoints: reader output (`:134-138`), persisted storage (`:178-180`), rendered HTML (`:197-198`). Header: "If this test ever fails, the privacy guarantee is broken — do not merge" (`slice_test.go:9-10`). Extend the SAME canary, SAME three checkpoints, to each new path:

- **Object-name path:** `CREATE TABLE "patients_PHI-CANARY-LEAK-9c2e3a" (id INT, "email_PHI-CANARY-LEAK-9c2e3a" TEXT)`. Run the inventory reader; assert the canary IS present (object names are T1 identifiers, legitimately round-trip) when `ignore_schema_regexp` does not match, AND a paired sub-test with the regexp set asserts the name is then ABSENT (proves the identifier-vs-literal boundary and the redaction knob).
- **Log-line path:** emit a log line whose RAW text contains the canary (e.g. `'PHI-CANARY-LEAK-9c2e3a@example.com'`). Assert the produced `LogEvent`, the storage rows, and the dashboard contain ONLY `event_type`/classification and NEVER the canary (proves raw line dropped to T2).
- **Plan path:** force an `auto_explain` plan whose `Filter`/`Index Cond`/`Output` references the canary. Assert `normalized_condition`, every JSONB `plan_tree` field in storage (`0004_query_plans.sql:19`), and the dashboard never contain the canary (proves fail-closed normalization, `plan.proto:58-61`).

Each path adds the same three `strings.Contains(x, canaryLiteral)` fatals at reader → storage → dashboard (mirroring `slice_test.go:135,178,197`), plus a positive "expected normalized/identifier form arrived" check (mirroring `sawPatientsSelect`, `slice_test.go:181-187`) so a pipeline that silently drops everything still fails.

**Known guardrail limit:** the contract test is a NAME allowlist, not a value scan — it cannot catch a literal smuggled inside an *allowed* field if the normalizer fails open. Defense rests on the fail-closed discipline (`plan.proto:58-61`, `normalize.go:40-49`) plus the canary. There is no meta-test asserting "every T1 message has a contract test" — recommended follow-up.

## 4. Component designs

### 4.1 Collector wiring + file-tail log source (ly-cxe.2)

**Goal:** Wire the built `logparse`/`planextract`/`insight` packages into the running collector. Add a rotation-aware file-tail `LogSource` that streams Postgres log bytes through `logparse.ParseStream`, then ships (a) classified T1 `LogEvent`s and (b) `auto_explain` plan bodies extracted to T1 `QueryPlan`s (optionally pre-scored by `insight.DetectPlans`) on a new collector ticker.

#### 4.1.1 `LogSource` interface + `FileTail` — new file `internal/collector/logsource.go`

`logparse.ParseStream(r io.Reader, opts) ([]LogEvent, []LogPayload, error)` (`framework.go:34`) drains `r` to EOF and returns parallel slices. A live log never EOFs, so the source hands `ParseStream` a *bounded* reader per poll: read everything appended since the last offset, advance.

```go
package collector

import "io"

// LogSource yields newly-appended log bytes since the previous call.
// io.EOF is never returned — an empty reader means "nothing new yet".
// Read is single-goroutine.
type LogSource interface {
    Read() (io.Reader, error)
    io.Closer
}
```

```go
// FileTail tails a single log file, surviving logrotate/copytruncate.
// Tracks (inode, size): inode change => rotation, size shrink => truncation;
// reopens from offset 0 in either case.
type FileTail struct {
    path     string
    f        *os.File
    inode    uint64
    offset   int64
    maxChunk int64 // cap bytes per Read; default 8 MiB
}

func NewFileTail(path string) *FileTail { return &FileTail{path: path, maxChunk: 8 << 20} }

func (t *FileTail) Read() (io.Reader, error)            // bounded chunk since last offset, cut on last '\n'
func (t *FileTail) reopenIfRotated() error              // os.Stat path; inode change => reopen, offset=0
func (t *FileTail) Close() error
```

Key behaviors (load-bearing — see Risks):
- `size < t.offset` ⇒ truncation (copytruncate), reset `offset = 0`.
- `n > t.maxChunk` ⇒ cap at 8 MiB so a backlog can't OOM.
- **Cut on newline:** `Read` truncates the returned slice at the last `'\n'` and rewinds `offset` accordingly, so each chunk ends on a record boundary — otherwise a TAB-prefixed stderr continuation record (`scanner.go:74-108`) straddling a chunk boundary is dropped by the parser (`framework.go:58-62`).
- Missing file ⇒ `errors.Is(err, fs.ErrNotExist)` returns empty reader + nil error (wait for file to appear).

`inodeOf` is a `//go:build`-guarded helper (`logsource_unix.go`) reading `fi.Sys().(*syscall.Stat_t).Ino` — both darwin (dev) and linux (prod) supply it; a non-Unix fallback returns 0 so cross-compiles don't break. No `fsnotify` dep — polling via `os.Stat` (`go.mod` confirms none present).

#### 4.1.2 Config — extend `config` + `loadConfig` (`cmd/collector/main.go:122-161`)

Add to the `config` struct and `loadConfig` (after `main.go:156`, before the required-var gate at `:157`), following the exact existing env-var pattern:

```go
logSourcePath   string        // LYNCEUS_LOG_SOURCE_PATH; "" disables log ingestion (default — strictly additive)
logSourceFormat string        // LYNCEUS_LOG_FORMAT: "csv" | "stderr" (default stderr)
logStderrPrefix string        // LYNCEUS_LOG_STDERR_PREFIX; default "%m [%p] "
logTailInterval time.Duration // LYNCEUS_LOG_TAIL_INTERVAL; default 2s
detectLocally   bool          // LYNCEUS_INSIGHT_LOCAL == "1"
```

`logSourcePath == ""` keeps the collector byte-for-byte identical to today, so this does not touch the required-var gate at `main.go:157-159`.

#### 4.1.3 Tail-consume pipeline — new file `internal/collector/log_pipeline.go`

```go
type LogPipeline struct {
    src           LogSource
    opts          logparse.Options
    serverID      string
    detectLocally bool
}
func NewLogPipeline(src LogSource, serverID string, opts logparse.Options, detectLocally bool) *LogPipeline

type DrainResult struct {
    LogEvents  []*lynceusv1.LogEvent
    QueryPlans []*lynceusv1.QueryPlan
    Insights   []insight.Insight // populated only when detectLocally; NOT shipped (logged count only)
}

func (p *LogPipeline) Drain() (DrainResult, error)
```

`Drain` reads one bounded chunk, calls `logparse.ParseStream` (`framework.go:34`), maps each T1 `events[i]` via `toProtoLogEvent`, and reuses `ExtractPlans(events, payloads)` (`plan_pipeline.go:25`) to turn T2 payload bodies into T1 plans. When `detectLocally`, calls `insight.DetectPlans(res.QueryPlans)` (`insight.go:68`). The privacy split is preserved: T1 `events` → `toProtoLogEvent`; T2 `payloads` only reach `ExtractPlans` (which drops literals).

The missing converter (none exists today) — copies ONLY T1 `logparse.LogEvent` fields (`event.go:13`), never reaching into `LogPayload`:

```go
func toProtoLogEvent(e logparse.LogEvent) *lynceusv1.LogEvent {
    return &lynceusv1.LogEvent{
        EventType:       string(e.EventType),  // event_type.go: string vocabulary
        Severity:        e.Severity.String(),  // severity.go:22 canonical name
        OccurredAtUnix:  e.OccurredAt.Unix(),
        LoggedAtUnix:    e.LoggedAt.Unix(),
        Pid:             e.PID,
        BackendType:     e.BackendType,
        DatabaseName:    e.DatabaseName,
        UserName:        e.UserName,
        ApplicationName: e.AppName,
        ClientAddrHash:  e.ClientAddrHash, // already SHA-256 hex at classifier.go:69
        SqlState:        e.SQLState,
        SessionLineNum:  e.SessionLineNum,
        TransactionId:   e.TransactionID,
    }
}
```

#### 4.1.4 Wiring in `cmd/collector/main.go`

- **Construct** (after the `shipper` line, `main.go:33`): build `*collector.LogPipeline` only when `cfg.logSourcePath != ""`; `format := logparse.FormatStderr` (or `FormatCSV` if `cfg.logSourceFormat == "csv"`, `scanner.go:15-18`); `tail := collector.NewFileTail(cfg.logSourcePath)`; `defer tail.Close()`. Adds `logparse` to the import block (`main.go:13-14`).
- **`runLogTail` closure** (after `flushActivity`, `main.go:95`): `logPipe.Drain()`; on error log and continue (partial results valid); skip if both slices empty; else build a `*lynceusv1.Snapshot{ ServerId, CollectedAtUnix, LogEvents: res.LogEvents (NEW field), QueryPlans: res.QueryPlans }` and `shipper.Send(ctx, snap)` (`shipper.go:29`); log `shipped %d log_events, %d query_plans (%d local insights)`.
- **Ticker + select arm** (beside the existing three, `main.go:101-119`): when `logPipe != nil`, `logTicker := time.NewTicker(cfg.logTailInterval)`; the arm `case <-logTickerC: runLogTail()`. When `logPipe == nil`, guard with `var logTickerC <-chan time.Time` left nil — a nil channel blocks forever in select, so the existing three-ticker behavior is byte-for-byte unchanged when no log source is configured.

#### 4.1.5 Proto change (literal-free)

Add to `proto/lynceus/v1/snapshot.proto` (currently fields 1-5 used; verified `query_stats=3`, `activity_buckets=4`, `query_plans=5`; imports only `plan.proto`):

```proto
import "proto/lynceus/v1/log_event.proto";   // add alongside existing plan.proto import
// ... inside Snapshot:
repeated LogEvent log_events = 8;   // see field-number reservation below
```

> **Snapshot field-number reservation (avoids collision across the three independent beads).** Fields 1–5 are used today. Layer 0 reserves **6 = `schema_objects` (ly-xqf.5), 7 = `table_stats` (ly-xqf.6), 8 = `log_events` (ly-cxe.2)**. These beads may land in separate PRs in any order; each PR MUST claim its reserved number and never reuse another's. (Proto field numbers need not be contiguous, so a gap is fine if one bead is deferred.)

REQUIRED — `Snapshot` has NO log-event field today, so classified events cannot ship without it. **Carries NO literal:** each element is a `lynceus.v1.LogEvent` (`log_event.proto:23`) whose every field is a fixed-vocabulary enum-string (`event_type`, `severity`, `sql_state`), a catalog identifier (`database_name`, `user_name`, `application_name`, `backend_type`), a SHA-256 hash (`client_addr_hash`), or a numeric counter. Raw message/detail/hint/statement live only in the collector-local T2 `LogPayload` (`payload.go:18`), never referenced here. No change to `LogEvent`/`QueryPlan` shapes — both already exist and are contract-tested. Regenerate via `make proto` so `Snapshot.LogEvents`/`GetLogEvents()` appear.

#### 4.1.6 Store / ingestion (scoping decision)

No new migration is strictly required for the collector wiring. For shipped `LogEvent`s to land, the ingestion server needs a handler symmetric to the existing ones: `snapshotToLogEvents(snap)` + `s.stats.WriteLogEvents(ctx, rows)` in `internal/ingest/server.go`, mirroring the query_plans block (`server.go:114-120`) and the converter (`server.go:170-181`), plus a partitioned `log_events` table + COPY writer following `WriteQueryPlans`/`QueryPlanRow` (`internal/store/plans.go:17,34`).

**Decision for ly-cxe.2:** ship the collector side; the ingestion server **parks shipped `LogEvent`s as a no-op** until a dedicated `log_events` store bead lands. State this explicitly so end-to-end persistence expectations are correct. (If the implementer elects to include the table + handler in ly-cxe.2, the `log_events` migration takes the **next free stats number after the schema/table migrations below — i.e. 0007** — to keep lexical ordering; see §4.2.)

#### 4.1.7 Risks
- New `Snapshot.log_events` field needs a parallel allowlist entry in `contract_test.go` (today only QueryStat is allowlisted at `:20-45`); `toProtoLogEvent` must copy ONLY `logparse.LogEvent` fields, never `LogPayload`.
- Newline-cut in `FileTail.Read` is correctness-critical (continuation/CSV-row splits dropped otherwise).
- Inode rotation is Unix-only — `//go:build`-guarded with a documented degraded path on non-Unix.
- Shutdown drops the final unshipped chunk after `offset` advanced — same class as the activity-bucket flush gap; **OUT OF SCOPE** (workstream B), flagged so it isn't mistaken for a regression.
- Ingestion silently ignores `LogEvent`s until the store bead lands (scoping decision above).
- New `logTicker` (2s) is a fourth `Send` cadence; `maxChunk` (8 MiB) bounds one poll but a log storm across ticks could be heavy — acceptable for Layer 0, follow-up bead worth filing.

### 4.2 Schema/object inventory + table-size/growth/TOAST readers (ly-xqf.5 + ly-xqf.6)

**Goal:** Two collector-side, read-only catalog readers that become the data foundation for Layer 1 (Index Advisor) and Layer 2 (VACUUM/config/checks). ly-xqf.5 walks system catalogs producing a structural inventory of schemas/tables/indexes/views/functions/sequences with sizes and a stable first-seen timestamp, gated behind a schema-name regex filter. ly-xqf.6 adds a per-table size/growth + TOAST/index/heap breakdown plus vacuum/dead-tuple metrics, stored as a weekly-partitioned time series so growth is derivable.

**Reconciliation with the existing plan** (`docs/superpowers/plans/2026-05-29-ly-xqf-5-schema-inventory.md`): keep its `SchemaObject` proto + `ObjectKind` enum, the `SchemaFilter` boundary (`internal/collector/inventory_filter.go`), the `inventory.go` reader, and the first-seen-preserving upsert (`internal/store/schema_objects.go`) **verbatim**. Two factual refinements:
1. **Migration numbering is stale.** The plan names `0003_schema_objects.sql`, but `0003`/`0004` are taken (`0003_activity_buckets.sql`, `0004_query_plans.sql` — confirmed on disk; the runner applies files in lexical order, `migrate.go:48` `sort.Strings(files)`). Inventory migration → **`0005_schema_objects.sql`**; ly-xqf.6 → **`0006_table_stats.sql`**.
2. **The plan's `schema_objects` table is a current-state upsert (PK `(server_id, kind, fqn)`)** — correct for first-seen but gives no growth-over-time. ly-xqf.6's "size & growth" requirement needs a *time series*, so it gets its own weekly-partitioned `table_stats` table following the `stats.go` COPY pattern (`stats.go:60-91`). The two coexist.

#### 4.2.1 Proto messages — extend `proto/lynceus/v1/snapshot.proto` (fields 6, 7)

```proto
enum ObjectKind {
  OBJECT_KIND_UNSPECIFIED = 0;
  OBJECT_KIND_SCHEMA      = 1;
  OBJECT_KIND_TABLE       = 2;  // includes partitioned tables + materialized views
  OBJECT_KIND_INDEX       = 3;
  OBJECT_KIND_VIEW        = 4;
  OBJECT_KIND_FUNCTION    = 5;
  OBJECT_KIND_SEQUENCE    = 6;
}

message SchemaObject {
  ObjectKind kind           = 1;  // enum
  string     schema         = 2;  // namespace IDENTIFIER (filtered at collector boundary)
  string     name           = 3;  // relation/proc IDENTIFIER ("" for kind=SCHEMA)
  string     fqn            = 4;  // "schema.name" IDENTIFIER — join key
  int64      size_bytes     = 5;  // SIZE (pg_total_relation_size / pg_relation_size)
  bool       is_partition   = 6;  // catalog FLAG (pg_class.relispartition)
  string     parent_fqn     = 7;  // partition-parent IDENTIFIER, "" if none (blanked if parent schema filtered)
  int64      first_seen_at_unix = 8; // TIMESTAMP from stats DB
}

message TableStat {
  string schema  = 1;  // IDENTIFIER (filtered at boundary)
  string name    = 2;  // table IDENTIFIER
  string fqn     = 3;  // "schema.name" IDENTIFIER — join key to SchemaObject

  int64 total_bytes   = 4;  // pg_total_relation_size(oid)            [SIZE]
  int64 heap_bytes    = 5;  // pg_table_size(oid) - toast_bytes        [SIZE]
  int64 toast_bytes   = 6;  // pg_total_relation_size(reltoastrelid)   [SIZE, 0 if no TOAST]
  int64 indexes_bytes = 7;  // pg_indexes_size(oid)                    [SIZE]

  int64 row_estimate       = 8;  // pg_class.reltuples (GREATEST(.,0)) [COUNT]
  int64 live_tuples        = 9;  // pg_stat_user_tables.n_live_tup     [COUNT]
  int64 dead_tuples        = 10; // n_dead_tup (bloat signal)          [COUNT]
  int64 n_mod_since_analyze= 11;

  int64 seq_scan      = 12; int64 idx_scan = 13;   // [COUNTERS]
  int64 n_tup_ins     = 14; int64 n_tup_upd = 15;
  int64 n_tup_del     = 16; int64 n_tup_hot_upd = 17;

  int64 last_vacuum_unix      = 18; int64 last_autovacuum_unix  = 19; // [TIMESTAMPS, 0 if never]
  int64 last_analyze_unix     = 20; int64 last_autoanalyze_unix = 21;
  int64 vacuum_count          = 22; int64 autovacuum_count      = 23; // [COUNTERS]
}

// In Snapshot:
repeated SchemaObject schema_objects = 6;  // ly-xqf.5
repeated TableStat    table_stats    = 7;  // ly-xqf.6
```

**Literal-free:** every field is enum / identifier / size / count / counter / unix-timestamp / bool-flag — no column values, defaults, constraint bodies, comments, ACLs, proc source, MCV/histogram bounds, predicates, or `relfrozenxid`. `TableStat` is the same privacy class as `ActivityBucket` (counts + labels), which the contract test already blesses. Regenerate via `make proto`.

#### 4.2.2 Postgres queries (read-only, RDS/Aurora-safe, narrow column lists)

ly-xqf.5 inventory: six narrow queries, one per kind (per the plan). The load-bearing tables query:

```sql
SELECT n.nspname, c.relname,
       COALESCE(pg_total_relation_size(c.oid),0)::bigint AS sz,
       c.relispartition,
       COALESCE(pn.nspname||'.'||pc.relname,'') AS parent_fqn
  FROM pg_class c
  JOIN pg_namespace n  ON n.oid = c.relnamespace
  LEFT JOIN pg_inherits  i  ON i.inhrelid  = c.oid
  LEFT JOIN pg_class     pc ON pc.oid      = i.inhparent
  LEFT JOIN pg_namespace pn ON pn.oid      = pc.relnamespace
 WHERE c.relkind IN ('r','p','m','f');
```

ly-xqf.6 table stats (`table_stats_reader.go` Read — NEW): one join computing the TOAST/heap/index split:

```sql
SELECT n.nspname, c.relname,
       pg_total_relation_size(c.oid)::bigint                              AS total_bytes,
       (pg_table_size(c.oid)
         - COALESCE(pg_total_relation_size(c.reltoastrelid),0))::bigint   AS heap_bytes,
       COALESCE(pg_total_relation_size(c.reltoastrelid),0)::bigint        AS toast_bytes,
       pg_indexes_size(c.oid)::bigint                                     AS indexes_bytes,
       GREATEST(c.reltuples,0)::bigint                                    AS row_estimate,
       COALESCE(s.n_live_tup,0), COALESCE(s.n_dead_tup,0), COALESCE(s.n_mod_since_analyze,0),
       COALESCE(s.seq_scan,0), COALESCE(s.idx_scan,0),
       COALESCE(s.n_tup_ins,0), COALESCE(s.n_tup_upd,0), COALESCE(s.n_tup_del,0), COALESCE(s.n_tup_hot_upd,0),
       s.last_vacuum, s.last_autovacuum, s.last_analyze, s.last_autoanalyze,
       COALESCE(s.vacuum_count,0), COALESCE(s.autovacuum_count,0)
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
 WHERE c.relkind IN ('r','p','m')
 ORDER BY n.nspname, c.relname;
```

`last_*` scan as nullable `*time.Time` → unix seconds, 0 on NULL. `GREATEST(reltuples,0)` guards the PG14+ `-1` (never-analyzed) case. The same `SchemaFilter.IsAllowed(schema)` check (the plan's `inventory_filter.go`) runs before each row becomes a proto — `TableStat` reuses the exact filter instance, so a filtered schema yields zero table-stat rows too.

#### 4.2.3 Go reader types (follow `reader.go:23-33` and `activity_reader.go:25-63`)

```go
// internal/collector/table_stats_reader.go
type TableStatsReader struct {
    pool   *pgxpool.Pool
    filter *SchemaFilter
}
func NewTableStatsReader(pool *pgxpool.Pool, filter *SchemaFilter) *TableStatsReader
func (r *TableStatsReader) Read(ctx context.Context, serverID string) ([]*lynceusv1.TableStat, error)
```

`Inventory` (ly-xqf.5) is exactly the plan's `internal/collector/inventory.go`, `NewInventory(pool, filter, firstSeen)` → `[]*lynceusv1.SchemaObject`.

#### 4.2.4 Store — migrations, writers, readers

**`0005_schema_objects.sql`** (the plan's table, renumbered): current-state upsert keyed `(server_id, kind, fqn)`, `first_seen_at` never overwritten on conflict; `last_seen_at`/`size_bytes_latest` refreshed. Store code `internal/store/schema_objects.go`: `NewSchemaObjects(pool)`, `SchemaObjectRow`, `UpsertSchemaObjects` (`INSERT … ON CONFLICT DO UPDATE` with `first_seen_at` omitted from the SET clause), `ListByServer`, `FirstSeenAt` — the plan's Task 3 verbatim.

**`0006_table_stats.sql`** (NEW, mirrors `0003_activity_buckets.sql`): weekly range-partitioned growth series.

```sql
CREATE TABLE table_stats (
    server_id          TEXT NOT NULL,
    collected_at       TIMESTAMPTZ NOT NULL,
    schema_name        TEXT NOT NULL,
    object_name        TEXT NOT NULL,
    fqn                TEXT NOT NULL,
    total_bytes        BIGINT NOT NULL,
    heap_bytes         BIGINT NOT NULL,
    toast_bytes        BIGINT NOT NULL,
    indexes_bytes      BIGINT NOT NULL,
    row_estimate       BIGINT NOT NULL,
    live_tuples        BIGINT NOT NULL,
    dead_tuples        BIGINT NOT NULL,
    n_mod_since_analyze BIGINT NOT NULL,
    seq_scan           BIGINT NOT NULL,
    idx_scan           BIGINT NOT NULL,
    n_tup_ins          BIGINT NOT NULL,
    n_tup_upd          BIGINT NOT NULL,
    n_tup_del          BIGINT NOT NULL,
    n_tup_hot_upd      BIGINT NOT NULL,
    last_vacuum        TIMESTAMPTZ,
    last_autovacuum    TIMESTAMPTZ,
    last_analyze       TIMESTAMPTZ,
    last_autoanalyze   TIMESTAMPTZ,
    vacuum_count       BIGINT NOT NULL,
    autovacuum_count   BIGINT NOT NULL,
    data_tier          SMALLINT NOT NULL DEFAULT 1
) PARTITION BY RANGE (collected_at);

CREATE INDEX table_stats_brin_time ON table_stats USING brin (collected_at);
CREATE INDEX table_stats_srv_fqn   ON table_stats (server_id, fqn, collected_at);
```

Store code `internal/store/table_stats.go` — copy of the `stats.go`/`plans.go` writer pattern:

```go
type TableStatRow struct {
    ServerID    string
    CollectedAt time.Time
    SchemaName, ObjectName, FQN string
    TotalBytes, HeapBytes, ToastBytes, IndexesBytes int64
    RowEstimate, LiveTuples, DeadTuples, NModSinceAnalyze int64
    SeqScan, IdxScan, NTupIns, NTupUpd, NTupDel, NTupHotUpd int64
    LastVacuum, LastAutovacuum, LastAnalyze, LastAutoanalyze time.Time // zero -> NULL
    VacuumCount, AutovacuumCount int64
    DataTier int16 // 0 -> coerced to 1
}
var tableStatsColumns = []string{ /* same order as proto/SQL */ }

func (s *Stats) WriteTableStats(ctx context.Context, rows []TableStatRow) error
func (s *Stats) EnsureTableStatsWeeklyPartition(ctx context.Context, ts time.Time) error
func (s *Stats) LatestTableStats(ctx context.Context, serverID string, asOf time.Time) ([]TableStatRow, error)
func (s *Stats) TableSizeSeries(ctx context.Context, serverID, fqn string, since, until time.Time) ([]TableStatRow, error)
```

`WriteTableStats` = `CopyFromSlice` + `EnsureTableStatsWeeklyPartition` per distinct ISO week — identical control flow to `WriteActivityBuckets` (`stats.go:254-281`) and `WriteQueryPlans` (`plans.go:34-65`). Reuses `isoWeekBounds` (`stats.go:192`) + a `tableStatsPartitionName(ts)` formatted `table_stats_%04d_%02d`. Read methods filter `data_tier = 1`, served from `s.ro` (`stats.go:104` pattern).

#### 4.2.5 Ingestion (`internal/ingest/server.go`)

Add two converters next to `snapshotToActivityBuckets` (`server.go:183-201`) and two guarded write calls after the query-plans block (`server.go:114-120`):

```go
if objs := snapshotToSchemaObjects(&snap); len(objs) > 0 {
    if err := s.schemaObjects.UpsertSchemaObjects(ctx, objs); err != nil { /* parkDLQ + close */ }
}
if ts := snapshotToTableStats(&snap); len(ts) > 0 {
    if err := s.stats.WriteTableStats(ctx, ts); err != nil { /* parkDLQ + close */ }
}
```

`Server` gains a `schemaObjects *store.SchemaObjects` field (constructed in `cmd/ingestion/main.go` alongside `store.NewStats`). `snapshotToTableStats` maps `collected_at = time.Unix(snap.CollectedAtUnix,0).UTC()` (same fallback as `snapshotToRows`, `server.go:147-150`) and converts the four `last_*_unix` fields back to `time.Time` (0 → zero → NULL).

#### 4.2.6 Collector wiring (`cmd/collector/main.go`)

In `runFull` (`main.go:36-52`, the ~10m cadence where query stats already ship), after `reader.Read`, also call `inventory.Read(ctx, serverID)` and `tableStatsReader.Read(ctx, serverID)` and attach to `snap.SchemaObjects` / `snap.TableStats`. Build ONE shared `SchemaFilter` from `LYNCEUS_INCLUDE_SCHEMA_REGEXP` / `LYNCEUS_IGNORE_SCHEMA_REGEXP` (fail-fast on bad regex) and pass it to both `NewInventory` and `NewTableStatsReader` so the boundary is identical. The collector also holds a stats-DB pool + `store.SchemaObjects` for the first-seen lookup adapter (inventory only; table stats are pure point-in-time, persisted append-only on the ingestion side).

#### 4.2.7 Risks
- Migration numbering MUST be 0005/0006 or lexical ordering (`migrate.go:48`) misorders — called out explicitly.
- Two storage models for related data: `schema_objects` (upsert, first-seen) vs `table_stats` (append-only weekly partitions, growth) — intentionally separate tables; mixing loses either first-seen stability or growth history.
- `DropPartitionsOlderThan` (`stats.go`, hardcoded to `query_stats`) will NOT retire `table_stats` partitions until generalized — retention follow-up, out of scope but must be tracked or `table_stats` grows unbounded.
- `parent_fqn` leak: a partition child in an allowed schema could carry a `parent_fqn` pointing at a filtered schema — the plan already blanks it when the parent schema is filtered.
- TOAST/size functions take ACCESS-SHARE catalog locks and `stat()` files; on tens of thousands of relations this is non-trivial — keep on the slow (~10m) full cadence, never the ~10s activity cadence. Read-only / RDS-safe.
- Contract-test allowlist update must land in the SAME commit as the proto change (by design — the test is the gate).

### 4.3 Insight HTTP surfacing + plan visualization (ly-hnt + ly-xqf.10)

**Goal:** Surface the already-built insight engine and stored plans over HTTP as templ/HTMX SSR views, mirroring the existing dashboard/audit pattern. (1) An insights list/detail surface that loads stored plans via `store.TopPlansByQuery` and runs `insight.DetectPlans`, rendering each `Insight` with its Severity; (2) a plan-viz surface that recursively renders a `PlanNode` tree + a flat node grid. A thin, stateless caller over existing reads — no new business logic, T1-only on the wire. **No proto changes.**

#### 4.3.1 Routes (mirror `internal/api/server.go:40-48`)

Add four lines to `routes()` in the existing `GET /...` method-value style (Go 1.22 `ServeMux`):

```go
s.mux.HandleFunc("GET /insights",          s.handleInsightsPage)
s.mux.HandleFunc("GET /partial/insights",  s.handleInsightsPartial)
s.mux.HandleFunc("GET /plan",              s.handlePlanPage)
s.mux.HandleFunc("GET /partial/plan",      s.handlePlanPartial)
```

`withAuth` (`server.go:54-62`) already wraps the whole mux via `Handler()` (`server.go:38`), so these inherit the dev-auth gate and the 401 path exactly like `/audit`. Add a nav link `<a href="/insights">Insights</a>` in `Layout` (`web/layout.templ:47-50`).

#### 4.3.2 Handlers — new file `internal/api/insights.go`

Signatures match `func (s *Server) handleX(w http.ResponseWriter, r *http.Request)` (`audit.go:16,24`; `dashboard.go:11,19`); render is the same one-liner (`web.X(...).Render(r.Context(), w)`). `fetchInsights` enumerates plan keys, loads recent plans per key, runs the engine, maps to view-models; errors degrade to nil (`dashboard.go:30`, `audit.go:64` convention):

```go
func (s *Server) fetchInsights(r *http.Request) []web.InsightRow {
    now := time.Now().UTC()
    since := now.AddDate(0, 0, -30) // same 30d window as fetchTop (dashboard.go:27)
    keys, err := s.stats.ListPlanKeys(r.Context(), since, now, 200) // NEW store reader (§4.3.5)
    if err != nil { return nil }
    var out []web.InsightRow
    for _, k := range keys {
        plans, err := s.stats.TopPlansByQuery(r.Context(), k.ServerID, k.Fingerprint, since, now, 10) // plans.go:69
        if err != nil { continue }
        qps := make([]*lynceusv1.QueryPlan, 0, len(plans))
        for _, p := range plans { qps = append(qps, p.Plan) } // QueryPlanRow.Plan, plans.go:20
        for _, in := range insight.DetectPlans(qps) { // insight.go:68
            out = append(out, web.InsightRow{
                Kind: string(in.Kind), Severity: string(in.Severity), // insight.go:22-26,31-41
                Fingerprint: in.Fingerprint, Relation: in.Relation, NodePath: in.NodePath,
                RowsScanned: in.RowsScanned, RowsReturned: in.RowsReturned,
                Detail: in.Detail, // literal-free (insight.go:40 + slowscan.go:61)
                ServerID: k.ServerID,
            })
        }
    }
    return out
}
```

Plan-viz: `/plan?server=<id>&fp=<fingerprint>` → `fetchPlan` parses params (mirror `audit.go:38-39`), calls `TopPlansByQuery(..., 1)` (most-recent first, `plans.go` ORDER BY captured_at DESC), and returns `web.ToPlanVM(serverID, plans[0].Plan)` (empty `PlanVM{ServerID, Fingerprint}` → "no plan" branch). All proto getters used by the mapper are nil-safe (`plan.pb.go:85-309`): `GetRoot`, `GetNodeType`, `GetRelationName`, `GetIndexName`, `GetJoinType`, `GetPlanRows`, `GetActualRows`, `GetActualLoops`, `GetTotalCost`, `GetActualTotalTimeMs`, `GetRowsRemovedByFilter`, `GetPlans` — none carry a literal (contract enforced `plan.pb.go:9-13`).

#### 4.3.3 View-models + templ

New `web/insights.templ` — `InsightRow` view-model lives in the `web` package (like `AuditRow` at `audit.templ:8-27`). `InsightsPage` wraps `@Layout(...)` (`layout.templ:18`) around `InsightsTable`, which is an HTMX `outerHTML` self-reswap fragment (`hx-get="/partial/insights" hx-trigger="every 10s"`, the `QueriesTable` pattern `queries.templ:15-16`). Table render (head/body, `fmt.Sprintf` numerics, `<code>` identifiers, `.num` class) copied from `AuditTable` (`audit.templ:77-109`). Each row links to `/plan?server=<id>&fp=<fp>` via `templ.URL(...)`.

New `web/plan.templ` — `PlanVM`/`PlanNodeVM` view-models + `PlanPage`/`PlanView` (HTMX fragment) + a self-recursive `PlanTreeNode` component (the direct analogue of `insight.walkPath`, `insight.go:79-95`) rendering the tree, plus a flat grid over `vm.Flat`. `ToPlanVM` + `flatten` live in plain-Go `web/plan_vm.go` (depth-first, visit node then recurse `GetPlans()`) so the proto import stays out of the `.templ` and the `api` handler. Add `.badge.sev-high/medium/low` + `ul.plan-tree` CSS to the `Layout` `<style>` block (`layout.templ:26-43`).

`web/*_templ.go` is generated by `templ generate` (DO-NOT-EDIT header, `queries_templ.go:1`) — produces `web/insights_templ.go`, `web/plan_templ.go`. No change to `NewServer` (`server.go:31`) or `cmd/api/main.go:65-67` — handlers reuse the injected `s.stats` (read-replica-aware via `WithReadPool`, so reads hit `s.ro`).

#### 4.3.4 Store change — `ListPlanKeys` on `*store.Stats` (`internal/store/plans.go`)

The one genuine store gap: `TopPlansByQuery` (`plans.go:69`) is keyed by `(serverID, fingerprint)`, so the insights LIST page needs a way to enumerate which keys exist.

```go
type PlanKey struct{ ServerID, Fingerprint string }

func (s *Stats) ListPlanKeys(ctx context.Context, since, until time.Time, limit int) ([]PlanKey, error)
// SELECT DISTINCT server_id, fingerprint FROM query_plans
//  WHERE captured_at >= $1 AND captured_at < $2 AND data_tier = 1
//  ORDER BY server_id, fingerprint LIMIT $3
// runs on s.ro (replica), exactly like TopPlansByQuery (plans.go:72)
```

**Literal-free:** `server_id` + `fingerprint` are structural identifiers — the same allowlist already written by `WriteQueryPlans` (`plans.go:25-28`). No migration/table change: `query_plans` already exists and is weekly-partitioned. Read-path only.

#### 4.3.5 Risks
- `fetchInsights` does N+1 reads (`ListPlanKeys` then one `TopPlansByQuery` per key) — bounded by `LIMIT 200` keys × 10 plans, replica-served; the limit constants are deliberate, not accidental. A single-query join is a future replacement.
- The `/plan` link string-concats `server`+`fp` into a query string; identifiers carry no literals (`plan.pb.go:9-13`) but should still be URL-escaped in `ToPlanVM`/handler — `templ.URL` handles HTML-attribute escaping, not query-param encoding.
- No time-window filter form yet; 30-day window hardcoded to match `fetchTop` — acceptable for MVP, likely fast-follow.
- `web/*_templ.go` must be regenerated with `templ generate` after adding the new templ files; a CI check that generated output is current is assumed.

### 4.4 Capability matrix API + reader-gate retrofit (ly-xnk.4 + ly-xnk.3)

**Goal:** Expose a per-server capability matrix (discovered availability × effective policy × final-enabled) over GET, plus an audited POST toggle reusing the existing audited writer. Add a cheap effective-policy gate that every collector reader consults before issuing its query — so no row is constructed or shipped for a disabled capability — baked into the new schema/table readers (ly-xqf.5/.6) and retrofitted onto the stat_statements and stat_activity readers.

#### 4.4.0 Critical architecture constraint (drives the gate design)

The collector connects ONLY to the monitored Postgres + a websocket — it has **no config-DB handle** (`cmd/collector/main.go:24-33` builds the monitored `pool` + `Shipper`; no `store.NewConfig` anywhere under `internal/collector/` or `cmd/collector/`). So `caps.Allowed(...)` cannot read `capability_policy` synchronously from inside a reader without a per-read config-DB network round trip — which the bead forbids ("keep it cheap"). **Consequence:** the gate is a **collector-local in-memory snapshot** of effective policy, refreshed on the existing full-snapshot cadence (`cfg.interval`, default 10m, `main.go:101` `fullTicker`). The authoritative resolver `store.Config.EffectiveCapability` (`capability_policy.go:168`) does override-beats-default in one round trip and is the server-side source of truth the matrix API and the policy-snapshot endpoint both use.

#### 4.4.1 Part A — Capability matrix API (ly-xnk.4)

New file `internal/api/capabilities.go`, wired like the audit handlers. Routes (add to `routes()`, `server.go:45`):

```go
s.mux.HandleFunc("GET  /api/servers/{id}/capabilities",        s.handleCapabilityMatrix)
s.mux.HandleFunc("POST /api/servers/{id}/capabilities/{cap}",  s.handleCapabilityToggle)
```

Read wildcards with `r.PathValue("id")`; auth inherited from `withAuth` (`server.go:54`) — 401 when `DevAuth` off, like every route.

**GET handler — matrix DTO:**

```go
type capabilityCellDTO struct {
    Capability       string `json:"capability"`
    DatabaseName     string `json:"database_name"`     // "" = server-wide
    DiscoveredAvail  bool   `json:"discovered_available"`
    DiscoveredReason string `json:"discovered_reason"` // bounded, package-authored
    PolicyEnabled    *bool  `json:"policy_enabled"`    // nil = no explicit policy row
    PolicySource     string `json:"policy_source"`     // "server-default"|"database-override"|""
    FinalEnabled     bool   `json:"final_enabled"`     // discovered && effective(default-enabled)
}
type capabilityMatrixDTO struct {
    ServerID string              `json:"server_id"`
    Cells    []capabilityCellDTO `json:"cells"`
}
```

Handler joins, in Go, `s.conf.ListDiscoveredCapabilities(ctx, serverID)` (NEW, §4.4.3), `s.conf.ListCapabilityPolicies(ctx, serverID)` (`capability_policy.go:198`), and `caps.Declared()` (`caps.go:40`) for the capability axis (so every declared capability appears even with no discovery/policy row — the `Discover` completeness invariant, `caps.go:83`). `FinalEnabled = DiscoveredAvail && effective`, where **absent policy ⇒ enabled** (ly-xnk.3 default). `store.EffectiveCapability` returns `found=false` and lets the caller encode the default (`capability_policy.go:180`). Encode `Content-Type: application/json`.

**POST handler — audited toggle (reuses ly-xnk.2 writer verbatim):**

```go
type toggleRequestDTO struct {
    DatabaseName string `json:"database"` // "" => server-wide default
    Enabled      bool   `json:"enabled"`
    Reason       string `json:"reason"`
}
// got, err := s.conf.SetCapabilityPolicy(r.Context(), store.SetCapabilityPolicyInput{
//     ServerID: serverID, DatabaseName: req.DatabaseName, Capability: capability,
//     Enabled: req.Enabled, SetBy: actorFromContext(r), Reason: req.Reason })
```

`SetCapabilityPolicy` (`capability_policy.go:42`) already appends the tamper-evident audit row first via `AppendAuditReturning` (`config.go:158`, the `pg_advisory_xact_lock` chain) then upserts the policy row carrying `audit_chain_id` — so the POST gets the audited toggle for free. Optionally reject unknown capabilities against `caps.Declared()`. Under `DevAuth`, `actorFromContext` returns the constant `"dev-admin"` (real OIDC actor wiring is the Milestone-5 follow-up).

#### 4.4.2 Part B — Effective-policy gate (ly-xnk.3)

**B1. Server-side resolver helper** (`internal/caps/policy.go`) — turns the store resolver into ly-xnk.3 default-enabled semantics:

```go
type PolicyResolver interface {
    EffectiveCapability(ctx context.Context, serverID, db, capability string) (bool, store.PolicySource, bool, error)
}
// Allowed: per-db override ?? server default ?? ENABLED.
func Allowed(ctx context.Context, r PolicyResolver, serverID, db string, c Capability) (bool, error) {
    enabled, _, found, err := r.EffectiveCapability(ctx, serverID, db, string(c))
    if err != nil { return false, err }
    if !found { return true, nil }   // absent policy => enabled
    return enabled, nil
}
```

`*store.Config` already satisfies `PolicyResolver` (`capability_policy.go:168`).

**B2. Collector-local cached gate** (`internal/caps/gate.go`) — what readers call (the cheap path):

```go
type Gate struct {
    mu        sync.RWMutex
    enabled   map[gateKey]bool   // (db, capability) -> effective enabled
    fetchedAt time.Time
}
type gateKey struct{ db string; cap Capability }
func NewGate() *Gate

// Allowed: O(1) map lookup under RLock, zero I/O. Absent key => true
// (default-enabled): a fresh/unrefreshed collector fails OPEN, never silently dark.
func (g *Gate) Allowed(db string, c Capability) bool
// Replace: atomic swap of a freshly-fetched snapshot; called on fullTicker, NOT per read.
func (g *Gate) Replace(snap map[gateKey]bool)
```

**B3. How the snapshot reaches the collector** — the collector has no config DB, so add a JSON GET on the api server:

```go
s.mux.HandleFunc("GET /api/servers/{id}/policy-snapshot", s.handlePolicySnapshot)
```

Returns `[{capability, database_name, enabled}]` built from `ListCapabilityPolicies` (`capability_policy.go:198`) — only enum capability strings + booleans + the operator-supplied `database_name` (already stored, not monitored-DB-derived). The collector GETs this on `fullTicker` and calls `g.Replace(...)`. (Rejected alternative: websocket control frame — heavier proto surface for no benefit this milestone.)

**B4. Wiring the gate into readers** — each reader gains a `gate *caps.Gate` + a `db string` (the connection's `current_database()`):

```go
func (r *Reader) Read(ctx context.Context) ([]*lynceusv1.QueryStat, error) {
    if !r.gate.Allowed(r.db, caps.PgStatStatements) { // NEW gate check before the query
        return nil, nil  // disabled: build & ship nothing
    }
    // ...unchanged query at reader.go:34...
}
```

- `pg_stat_statements` reader → `caps.PgStatStatements` (gate before `reader.go:34`).
- `pg_stat_activity` reader → `caps.PgStatActivityFullRead` (gate before `activity_reader.go:37`). This reader groups per-`datname` (`activity_reader.go:38`); the gate is checked once with the connection's own database — DB-level activity policy is server-scoped here because one connection can't pre-filter other databases' rows. **Documented limitation.**
- **NEW schema/table readers (ly-xqf.5/.6)** are born with the gate, each checking its OWN capability (a `Capability` is a single value, not a bitmask): the inventory reader runs `if !r.gate.Allowed(r.db, caps.SchemaInventory) { return nil, nil }` and the table-stats reader `if !r.gate.Allowed(r.db, caps.TableSize) { return nil, nil }` before touching `pg_class`. Add two capability constants to `caps.Declared()` (`caps.go:40`) — `SchemaInventory`, `TableSize` — plus always-available probes (catalog-read only) so they appear in the matrix.
- **Not gated:** `plan_pipeline.go`/auto_explain extraction operates on already-parsed log records, not a live monitored-DB query — no reader query to gate. A future log-source reader issuing a query inherits the same pattern.

**B5. Collector main wiring** (`cmd/collector/main.go`): after `pool` is built (`main.go:24`), create `gate := caps.NewGate()`, resolve `db` once via `SELECT current_database()`, pass into `NewReader(pool, gate, db)` / `NewActivityReader(pool, gate, db)` (`main.go:30-31`), and add a `refreshPolicy` closure on `fullTicker` (`main.go:101`) that GETs `/policy-snapshot` and calls `gate.Replace`. Kick one refresh before the first `runFull()` (`main.go:98`) so the first snapshot already respects policy.

#### 4.4.3 Store changes

- **NEW migration `internal/store/migrations/config/0004_discovered_capability.sql`** (config has 0001-0003 on disk; 0004 is next): `discovered_capability(server_id TEXT REFERENCES servers(id) ON DELETE CASCADE, database_name TEXT, capability TEXT NOT NULL, available BOOLEAN NOT NULL, reason TEXT NOT NULL DEFAULT '', observed_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE NULLS NOT DISTINCT (server_id, database_name, capability))`. Mirrors `0003_capability_policy.sql`'s NULLS-NOT-DISTINCT uniqueness (`:19/:28`). Persists `caps.Set` from `Discover` (today computed at `caps.go:90` but never stored) so the matrix GET has a "discovered" column to join.
- **NEW store methods** `internal/store/discovered_capability.go`: `UpsertDiscoveredCapabilities(ctx, serverID, db string, set caps.Set)` and `ListDiscoveredCapabilities(ctx, serverID) ([]DiscoveredCapability, error)` (List reads `c.ro`, like `ListCapabilityPolicies`, `capability_policy.go:199`). The upsert is invoked by the discovery-ingest path (ly-xnk.2 wiring) when a discovery result arrives.
- **NO new method** for effective resolution or the toggle: `EffectiveCapability` (`:168`), `ListCapabilityPolicies` (`:198`), `SetCapabilityPolicy` (`:42`) already cover the gate resolver, the matrix policy column, and the audited POST.
- Confirm `internal/store/migrate.go` embeds the `config/` dir so `0004` is auto-discovered (same way `0003` is picked up throughout `capability_policy_test.go`).

#### 4.4.4 Proto changes

**None.** The gate is enforced collector-side (no row built for a disabled capability), so the Snapshot proto is unchanged — it never needs a `capability` marker. The policy snapshot travels as a JSON GET, not over the wire. (If later moved onto the websocket: a control message of `{capability_string, database_name, enabled_bool}` — still literal-free, closed enum + bool + operator-supplied identifier — but no proto change this milestone.)

#### 4.4.5 Risks
- **Collector has no config-DB handle** — the gate depends on the NEW `/policy-snapshot` fetch path; without it the collector cannot learn policy. The single biggest hidden scope item in ly-xnk.3; must not be assumed already wired.
- **Cache staleness:** a toggle takes up to `cfg.interval` (10m default) to reach the collector. Disabling a capability for privacy is not immediate — mitigate by refreshing on the faster activity interval or a dedicated shorter policy ticker; tuning decision.
- **Fail-open default** (absent ⇒ enabled): a fresh collector or unreachable api keeps collecting until the first successful policy fetch. If the operator expects "disabled by default until I opt in", confirm intended default direction with the bead owner before coding.
- **Discovered-capability persistence** is assumed by the matrix GET but does not exist yet (`caps.Discover` computes, nothing stores) — this component adds both the table and the upsert path.
- **Per-database activity granularity** is limited — a db-specific activity toggle for a different database than the collector connects to is not honored; needs product sign-off or a per-database connection model.
- **Actor identity** is the `dev-admin` stub under `DevAuth`; audit entries written now won't attribute to a real principal — acceptable for dev, the chain (`config.go:158`) records the placeholder.

## 5. Data flow

```
  MONITORED POSTGRES (read-only)                    COLLECTOR (outbound-only)
  ────────────────────────────                      ─────────────────────────
  pg_stat_statements ──► Reader ─────────[gate?]──┐
  pg_stat_activity   ──► ActivityReader ─[gate?]──┤
  pg_class/pg_namespace/pg_index/pg_proc           │   runFull (~10m) builds
        └────────────► Inventory ───────[gate]─────┤   ┌─ Snapshot{ query_stats(3),
  pg_class+pg_stat_user_tables                      │   │   activity_buckets(4), query_plans(5),
        └────────────► TableStatsReader [gate]──────┤   │   schema_objects(6), table_stats(7) }
                                                     │   │
  Postgres LOG FILE ──► FileTail.Read (bounded,      │   logTick (~2s):
     rotation/trunc aware, cut on '\n')              │   ┌─ ParseStream ─► []LogEvent (T1) ─► toProtoLogEvent
        └──────────────────────────────────────────►├──►│              └► []LogPayload (T2, collector-local)
                                                     │   │                    └► ExtractPlans ─► []QueryPlan (T1)
                                                     │   │  [detectLocally?] insight.DetectPlans (logged count only)
                                                     │   └─ Snapshot{ log_events(8 NEW), query_plans(5) }
            Gate.Replace ◄─ GET /api/servers/{id}/policy-snapshot (JSON, on fullTicker)
                                                     │
                                          shipper.Send(ctx, *Snapshot)  ── websocket ──►
                                                                                         │
  INGESTION SERVER                                                                       ▼
  ────────────────  handle(snap): snapshotToRows / ToActivityBuckets / ToQueryPlans /
                    ToSchemaObjects / ToTableStats / (ToLogEvents = no-op until store bead)
                       │        │              │             │              │
                       ▼        ▼              ▼             ▼              ▼
  STATS DB:        query_stats activity_buckets query_plans  table_stats   [log_events: parked]
                       (weekly partitions, data_tier=1, COPY writers)
  CONFIG DB:       schema_objects (upsert, first-seen)   discovered_capability   capability_policy + audit_log

  API SERVER (templ/HTMX SSR, read-replica via s.ro)
  ──────────────────────────────────────────────────
   GET /insights ─► ListPlanKeys ─► TopPlansByQuery ─► insight.DetectPlans ─► InsightsTable
   GET /plan?server=&fp= ─► TopPlansByQuery(...,1) ─► ToPlanVM ─► PlanTreeNode (recursive) + grid
   GET /api/servers/{id}/capabilities ─► join(discovered × policy × Declared) ─► matrix DTO
   POST .../capabilities/{cap} ─► SetCapabilityPolicy (audit row first, then upsert)
```

## 6. Testing strategy

All integration tests use **real Postgres via testcontainers** (`postgres:16`), `t.Skipf` when Docker is unavailable — no mocks (repo convention). Pattern sources cited inline.

**Contract tests (the build-time gate — must stay green), `internal/proto/lynceus/v1/contract_test.go`:**
- `TestSchemaObjectHasOnlyStructuralFields`, `TestSchemaObjectFieldKinds`, `TestSnapshotCarriesSchemaObjects` (ly-xqf.5, from the plan) via `assertOnlyAllowed` (`contract_test.go:158`).
- NEW `TestTableStatHasOnlyAggregateFields` (allowlist of all 23 `TableStat` field names), `TestTableStatScalarFieldShapes` (string fields `string` kind, byte/count fields `int64`), `TestSnapshotCarriesTableStats` — mirror `TestActivityBucketHasOnlyAggregateFields` (`:108-152`).
- NEW `TestSnapshotCarriesLogEvents` + a `LogEvent`/`Snapshot.log_events` allowlist entry (ly-cxe.2) — the Snapshot surface is not allowlisted today.

**ly-cxe.2** (`internal/collector/logsource_test.go`, `log_pipeline_test.go`, `test/e2e/log_slice_test.go`):
- `FileTail`: two-batch append (only-new bytes), rotation (rename + new inode → reopen at 0), truncation (size shrink → offset reset), record-boundary (partial line withheld until `'\n'`), missing-file (empty reader + nil err).
- `LogPipeline.Drain` + `toProtoLogEvent`: fake `LogSource` over the auto_explain fixture (`plan_pipeline_test.go:11`) → assert `LogEvents` mapped (`event_type`/`severity`/`pid`) and `QueryPlans` has the plan with non-empty fingerprint, no literal in `normalized_condition` (mirror `plan_pipeline_test.go:44-50`); `detectLocally=true` slow-scan fixture → `Insights` non-empty, literal-free `Detail`; privacy: marshal each `LogEvent` proto, assert a planted canary (lived only in the T2 payload) never appears in the bytes.
- e2e: `postgres:16` with `auto_explain` preloaded + `log_min_duration=0` + `log_format=json` + `logging_collector=on`, tail the file, run a canary query, drive one `Drain` + `shipper.Send` to a real `ingest.Server` (constructed as in `slice_test.go`) → assert a QueryPlan row persisted with a fingerprint + Seq node, AND the canary appears NOWHERE in persisted plan JSON or any shipped `LogEvent` proto bytes; rotation-under-load: rename mid-run, assert no events lost, no panic. `t.Skipf` like `slice_test.go:51`.

**ly-xqf.5 / ly-xqf.6** (`internal/store/{schema_objects,table_stats}_test.go`, `internal/collector/{inventory,table_stats_reader}_test.go`) — pattern `store_test.go:22-48` (`newPool`) + `:342-398` (partition round-trip):
- Store: `TestSchemaObjects_FirstSeenIsStableAcrossUpserts` (re-upsert with new size → `first_seen_at` unchanged, `last_seen_at`/size refreshed); `TestWriteTableStats_createsPartitionAndRoundtrips` (Wednesday ts → weekly partition created via `pg_inherits` count, `LatestTableStats` returns rows with correct `toast/heap/total`); `TestTableSizeSeries_growth` (two snapshots a week apart → both returned in time order, proves growth foundation); `TestWriteTableStats_emptyNoop` (nil → nil).
- Reader: inventory test (seed `reporting` + `patient_phi`, filter `NewSchemaFilter("","^patient_.*")`, assert all `reporting.*` present + ZERO `patient_phi` objects); `TestTableStatsReader_SizesAndToast` (`reporting.big` with `repeat('x',100000)` to force TOAST → `toast_bytes>0`, `heap_bytes>0`, `total≈heap+toast+indexes`, dead/live populated after UPDATE+ANALYZE, no `patient_phi`); `SchemaFilter` unit suite (default-allow, ignore-excludes, include-allowlist, ignore-wins, always-skip-system, invalid-regexp-errors).
- Ingestion: send a Snapshot with both `schema_objects` + `table_stats`, assert rows land in both tables, malformed write parks to DLQ (`server.go:135-144`).

**ly-hnt / ly-xqf.10** (`internal/api/{insights,plan}_test.go`, package `api_test`) — harness `server_test.go:21-62` (`newPGPool`/`setup`); NEW `seedPlans(t,pool)` mirroring `seedStats` (`server_test.go:80-93`) reusing the fixture `store/plans_test.go:40-66` with a `Seq Scan` child (`ActualLoops>0`, large `RowsRemovedByFilter`, tiny `ActualRows`) so `DefaultSlowScan` fires (`slowscan.go:18`), `CapturedAt = now-1h`:
- `TestInsightsPage_rendersDetectedInsights` (200, `text/html`, body has `<!doctype html>`, `id="insights-table"`, `hx-get="/partial/insights"`, nav `href="/insights"`, the seeded relation, a severity token); `TestInsightsPartial_returnsFragmentOnly` (no doctype, has `id="insights-table"`); `TestInsights_withoutDevAuth_returns401` (copy `audit_test.go:87`); **privacy** (copy `dashboard_test.go:43-51`): no banned literal substring in HTML.
- `TestPlanPage_rendersTreeAndGrid` (200, `id="plan-view"`, `class="plan-tree"`, root + child node types both present, grid `<th>Plan rows`); `TestPlanPartial_returnsFragmentOnly`; `TestPlan_missingKey_rendersEmpty` (`No plan stored` branch).
- Store: `TestListPlanKeys_returnsDistinctKeys` (two plans one key + one second key → exactly two distinct rows).

**ly-xnk.4 / ly-xnk.3** (`internal/api/capabilities_test.go`, `internal/caps/gate_test.go`, extend `capability_policy_test.go` + `reader_test.go`):
- API: `TestCapabilityToggle_devAuth_writesPolicyAndAudit` (POST → 200, assert `capability_policy` row + one `audit_log` row `action='capability_policy.set'`, mirror `capability_policy_test.go:109-120`); `TestCapabilityToggle_withoutDevAuth_returns401`; `TestCapabilityMatrix_joinsDiscoveredAndPolicy` (discovered=true + policy=false → cell `final_enabled=false`; second cap discovery-only → `policy_enabled` null, follows discovered); `TestCapabilityMatrix_absentPolicyDefaultsEnabled`.
- Gate (pure in-memory, run under `-race`): `TestGate_AbsentKeyFailsOpen`, `TestGate_ReplaceThenDisabled` (per-db scoping), `TestGate_ConcurrentReadDuringReplace`.
- Resolver (real DB): `TestAllowed_overrideBeatsDefault_andAbsentEnabled` (reuse `capability_policy_test.go:191` seeding).
- Reader gate (real DB): `TestReader_gatedOff_returnsNoRows` (gate pre-disabled → `Read` returns `nil,nil`, never issues the query even with data present; flip on → rows return — proves the gate, not query failure, suppressed output).

**Extended e2e canary (`test/e2e/slice_test.go`, cross-cutting):** add the object-name, log-line, and plan paths from §3.5 — same three checkpoints (reader → storage → dashboard), same `strings.Contains` fatals, plus a positive "expected normalized/identifier form arrived" assertion per path and the `ignore_schema_regexp`-on sub-test for the object-name path.

**Bead → acceptance test map:**

| Bead | Acceptance test(s) |
|---|---|
| ly-cxe.2 | `logsource_test.go` (FileTail), `log_pipeline_test.go` (Drain + privacy), `log_slice_test.go` (e2e canary + rotation), `contract_test.go` Snapshot/LogEvent allowlist |
| ly-xqf.5 | `schema_objects_test.go` (first-seen stable), `inventory_test.go` (filter privacy), `contract_test.go` SchemaObject |
| ly-xqf.6 | `table_stats_test.go` (partition round-trip, growth series), `table_stats_reader_test.go` (TOAST/sizes), `contract_test.go` TableStat |
| ly-hnt | `insights_test.go` (render + 401 + privacy), `plans_test.go::TestListPlanKeys_returnsDistinctKeys` |
| ly-xqf.10 | `plan_test.go` (tree + grid + recursion + empty-key) |
| ly-xnk.4 | `capabilities_test.go` (toggle+audit, 401, matrix join, default-enabled) |
| ly-xnk.3 | `gate_test.go`, `capability_policy_test.go::TestAllowed_*`, `reader_test.go::TestReader_gatedOff_returnsNoRows` |

## 7. Build sequence within Layer 0

Dependencies are intra-layer only — pull nothing from Layer 1.

1. **Proto + contract tests first (single commit each).** Add `Snapshot.log_events=8` + `log_event.proto` import (ly-cxe.2); `ObjectKind`/`SchemaObject`/`TableStat` + `schema_objects=6`/`table_stats=7` (ly-xqf.5/.6); regenerate via `make proto`; extend `contract_test.go` allowlists in the SAME commit. → verify: `go test ./internal/proto/...` green.
2. **Store layer (parallelizable across components).**
   a. `0005_schema_objects.sql` + `internal/store/schema_objects.go` (ly-xqf.5). → verify: `schema_objects_test.go`.
   b. `0006_table_stats.sql` + `internal/store/table_stats.go` (ly-xqf.6). → verify: `table_stats_test.go`.
   c. `ListPlanKeys` on `*store.Stats` (ly-hnt). → verify: `TestListPlanKeys_returnsDistinctKeys`.
   d. `0004_discovered_capability.sql` + `discovered_capability.go` (ly-xnk.3/.4). → verify: store test + migration auto-discovery.
3. **Collector readers + gate (prereq for end-to-end + the new readers' privacy filter).**
   a. `caps.Gate`/`Allowed`/`Replace` + `caps.Declared()` constants `SchemaInventory`/`TableSize` (ly-xnk.3). → verify: `gate_test.go` under `-race`.
   b. `Inventory` + `TableStatsReader` (born with the gate + shared `SchemaFilter`) (ly-xqf.5/.6). → verify: reader tests.
   c. Retrofit gate onto `Reader`/`ActivityReader` (ly-xnk.3). → verify: `TestReader_gatedOff_returnsNoRows`.
4. **In parallel with step 3 (no shared state):**
   a. `LogSource`/`FileTail` + `LogPipeline` + `toProtoLogEvent` (ly-cxe.2). → verify: `logsource_test.go`, `log_pipeline_test.go`.
   b. api handlers `/insights`, `/plan` + templ + `templ generate` (ly-hnt + ly-xqf.10). → verify: `insights_test.go`, `plan_test.go`.
   c. api handlers `/api/servers/{id}/capabilities`, `/policy-snapshot` (ly-xnk.4 + ly-xnk.3 B3). → verify: `capabilities_test.go`.
5. **Wiring (depends on 2-4).** Ingestion converters + write calls for schema_objects/table_stats (+ park log_events); collector `cmd/collector/main.go` constructs gate, readers, log pipeline, tickers, policy refresh. → verify: ingestion test, `go build ./...`.
6. **End-to-end + canary (depends on all above).** `log_slice_test.go` + extended `slice_test.go` canary paths. → verify: full e2e green, no canary leak.

**Critical path:** schema/table readers + caps gate (steps 2-3) are prereqs because the new readers are born with the gate and the privacy filter. Log source (4a) and the read-path surfacing (4b/4c) proceed in parallel once the proto + store land.

## 8. Definition of done

**Per-bead acceptance:**
- **ly-cxe.2** — collector with `LYNCEUS_LOG_SOURCE_PATH` set tails the file (rotation/truncation-safe, chunks cut on `'\n'`), ships T1 `LogEvent`s + extracted `QueryPlan`s on the log ticker; `LYNCEUS_LOG_SOURCE_PATH=""` leaves the collector byte-for-byte unchanged; `LogEvent` proto bytes never contain a canary planted in the raw line; ingestion log-event parking decision is documented.
- **ly-xqf.5** — inventory ships `SchemaObject`s for schemas/tables/indexes/views/functions/sequences with sizes + stable first-seen; `ignore_schema_regexp` excludes filtered schemas (zero filtered objects); `parent_fqn` blanked when the parent schema is filtered.
- **ly-xqf.6** — `TableStat`s ship the heap/toast/index split + dead-tuple/vacuum metrics; weekly-partitioned `table_stats` round-trips; `TableSizeSeries` returns two-week snapshots in order (growth derivable); TOAST forced and observed `>0`.
- **ly-hnt** — `/insights` renders detected insights with Severity from stored plans; `/partial/insights` is a fragment; 401 without dev-auth; no literal in rendered HTML.
- **ly-xqf.10** — `/plan?server=&fp=` renders the recursive `PlanNode` tree + flat grid; missing key → empty-state.
- **ly-xnk.4** — matrix GET returns discovered × policy × final-enabled per `(capability, database)`; POST toggle writes a `capability_policy` row + a tamper-evident `audit_log` row; absent policy ⇒ enabled.
- **ly-xnk.3** — every gated reader returns `nil,nil` (issues no query) when its capability is disabled in the gate; gate `Allowed` is lock+map only (no I/O); collector refreshes policy on `fullTicker`; absent key fails open.

**Cross-cutting (all must hold):**
- `go build ./...` clean; `make proto` produces current generated code; `templ generate` output committed and current.
- `go test ./...` green (integration via testcontainers; `t.Skipf` only when Docker is unavailable).
- **Contract test green** — every new T1 message (`SchemaObject`, `TableStat`) and new `Snapshot` field (`log_events`, `schema_objects`, `table_stats`) has a `assertOnlyAllowed` allowlist entry + string-kind guard, landed in the same commit as the proto change.
- **e2e canary green** — `slice_test.go` + `log_slice_test.go`: the canary literal appears NOWHERE at reader → storage → dashboard for the object-name (filtered), log-line, and plan paths, AND the expected normalized/identifier form did arrive.
- **No new literal-capable field** anywhere on a T1 message — every added field is an identifier, count, size, duration, enum, or fingerprint. No raw-text column added to any stats table; `data_tier DEFAULT 1` on every new table; T2 raw payloads remain collector-local.
