# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:970c3bf2 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   bd dolt push
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->


## Build & Test

```bash
make dev-up        # start dev databases (config Postgres + TimescaleDB) via docker-compose
make proto         # regenerate Go from proto/ (the wire contract)
go build ./...     # build all binaries (cmd/collector, cmd/ingestion, cmd/api)
go test ./...      # unit + integration tests (integration uses testcontainers)
make dev-down      # stop dev databases
```

## Architecture Overview

Lynceus is an open-source, Kubernetes-native, HA PostgreSQL monitoring platform — a privacy-first reimagining of pganalyze. Three Go services share `internal/` packages and a versioned protobuf wire contract:

- **collector** (`cmd/collector`) — runs near Postgres, outbound-only, as a limited DB role. Reads `pg_stat_*` / `auto_explain` / logs, **normalizes and analyzes locally**, ships only normalized (T1) data over a websocket.
- **ingestion_server** (`cmd/ingestion`) — terminates collector websockets, rate-limits, dead-letter-queues, writes to the TimescaleDB stats store.
- **api_server** (`cmd/api`) — OIDC/SCIM auth, RBAC, audit log, collector token issuance, config API; serves the templ+HTMX SSR frontend.

Two databases, both **vanilla PostgreSQL** (must run on AWS RDS/Aurora — no extensions): config/metadata + audit, and the stats store (native time-range partitioning managed in Go). TimescaleDB is an optional stats backend behind the `store.Stats` interface; **no feature may depend on it.**

**Full design:** `docs/specs/2026-05-29-lynceus-design.md`. **MVP plan:** `docs/superpowers/plans/2026-05-29-lynceus-mvp-vertical-slice.md`.

## Conventions & Patterns

- **Privacy is the backbone, not a feature.** Analysis happens at the collector; only normalized, literal-free data leaves the customer's infrastructure. The proto T1 message types contain **no field capable of carrying a literal value** — there is a contract test enforcing this. Never add a raw-sample/raw-text field to a T1 message.
- **Data classification:** T1 (normalized, broadly viewable) vs T2 (may contain literals — off by default per server, gated behind group RBAC, every read audited). The `data_tier` column and `audit_log` table exist from day one even where only T1 is produced.
- **Postgres access is read-only.** The collector never modifies the monitored database.
- TDD: write the failing test first (see the MVP plan). Integration tests hit real Postgres via testcontainers — do not mock the database.

## Feature Work Lifecycle (M2–M6)

Milestone-2+ features in beads are tracked as **discrete units of work, parallelizable once their blockers clear**. Each carries the `needs-plan` label until a plan is written.

When you claim a feature issue (`bd update <id> --claim`), expected lifecycle:
1. **Plan** — use the `superpowers:writing-plans` skill to produce a plan at `docs/superpowers/plans/<feature-slug>.md`. Commit the plan. Replace the `needs-plan` label with `ready-impl` (`bd label remove <id> needs-plan && bd label add <id> ready-impl`). At this point another worker may pick up implementation.
2. **Implement** — execute the plan TDD-style; commit per task.
3. **Test** — ensure the plan's test acceptance criteria pass; integration tests run against real Postgres (testcontainers), not mocks. Label `ready-test` → final verification → close.

Hand-offs between agents happen at the label transitions. Use `bd note <id>` to record context for the next worker before stepping away. Always run `bd ready` to find unblocked work; never claim a blocked issue.

**Feature specs:** `docs/specs/2026-05-29-lynceus-features.md` — every feature has a parity priority, data source, locality, and privacy classification.
