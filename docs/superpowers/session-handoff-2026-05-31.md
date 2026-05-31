# Session handoff — 2026-05-31

Snapshot of where the project is at the end of the 2026-05-29 → 2026-05-31 working session, plus exact steps for picking it back up on another machine. Repo HEAD at handoff: **`a180dec`**, fully pushed; beads-dolt synced; nothing left `in_progress`.

## Pickup on another machine — exact steps

**1. Install tools** (one-time):

```bash
# Go 1.26+, Docker (testcontainers needs it), protoc, dolt
brew install go docker protobuf dolt
# bd (beads)
curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash
```

**2. Clone + hydrate** (project state lives in the repo + Dolt remote):

```bash
git clone git@github.com:dobbo-ca/lynceus.git
cd lynceus
bd bootstrap     # pulls beads issue DB from refs/dolt/data on the git remote
```

**3. Install code generators** (idempotent; needed for `make gen` and CI parity):

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install github.com/a-h/templ/cmd/templ@latest
```

**4. Verify the environment** (testcontainers pulls `postgres:16` on first run):

```bash
make build               # builds the three binaries
go test ./... -timeout 10m   # runs the full suite, including e2e
```

**5. Find work:**

```bash
bd ready                              # everything unblocked, by priority
bd query 'label = "ready-impl"'       # the 4 features already planned
bd show ly-cxe.1                      # inspect one in detail
```

## Where the work stands

| Where | What |
|---|---|
| **Done** | M1 vertical slice — 9/9 tasks closed, M1 epic closed, CI wired. 10 issues closed. |
| **Plans written, awaiting impl** | `ly-cxe.1` log parsing · `ly-xqf.1` pg_stat_activity · `ly-xqf.5` schema inventory · `ly-8b0.3` tamper-evident audit log. All carry the `ready-impl` label. |
| **New, ready to plan** | `ly-xnk.1` capability discovery (no deps). The rest of the `ly-xnk` epic cascades behind it + the audit chain. |
| **Gated by deps** | M3 EXPLAIN insights wait on `ly-xqf.10` auto_explain, which waits on `ly-cxe.1` log parsing — making the log-parsing implementation the single highest-leverage next move. |

**Beads totals at handoff:** 98 issues · 10 closed · 29 ready · 59 blocked.

## Where everything lives in the repo

- **Design spec:** `docs/specs/2026-05-29-lynceus-design.md`
- **Feature catalog (≈80 features, MUST/SHOULD/COULD):** `docs/specs/2026-05-29-lynceus-features.md` (incl. §10b capabilities)
- **Plans (TDD-structured):** `docs/superpowers/plans/2026-05-29-*.md`
- **Workflow + privacy conventions:** `CLAUDE.md` — read the "Feature Work Lifecycle" section
- **Code:** `cmd/{collector,ingestion,api}/`, `internal/{normalize,store,collector,ingest,api,proto}/`, `web/`
- **Generated artifacts (committed):** `internal/proto/lynceus/v1/*.pb.go`, `web/*_templ.go` — CI regenerates and fails if they drift from sources

## Suggested first move on the new machine

After bootstrap, the highest-leverage single action is **implementing `ly-cxe.1`** (Postgres log parsing framework). Its plan is at `docs/superpowers/plans/2026-05-29-ly-cxe-1-log-parsing-framework.md`. It cascades through `ly-xqf.10` (auto_explain extraction) into all 8 M3 EXPLAIN insights — a long-reach unblock.

To claim it and follow the multi-agent lifecycle from `CLAUDE.md`:

```bash
bd update ly-cxe.1 --status in_progress
# follow the plan task-by-task TDD; close when done
bd close ly-cxe.1
bd label remove ly-cxe.1 ready-impl
git push && bd dolt push
```
