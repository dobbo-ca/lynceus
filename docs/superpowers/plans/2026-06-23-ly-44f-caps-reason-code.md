# ly-44f — Privacy guardrail: capability-probe Reason → literal-free reason CODES

## Problem

`internal/caps/probes.go` interpolates raw monitored-DB content into the
cross-boundary `Capability.Status.Reason` string: driver `err.Error()` (8 sites),
`%q` of raw `SHOW server_version_num` output, and the `pg_current_logfile()` path.
This is latent today (caps.Discover is unwired, Snapshot has no capability field),
but the CRD control-plane design (§4.6, `crd-operator-control-plane.md:316-332`)
reclassifies discovery write-back as a NEW T1 wire boundary carrying
`{capability, available, reason_code, observed_at}` where `reason_code` MUST be a
closed enum. Shipping the current free-text `Reason` the moment that boundary lands
leaks raw Postgres error text / version literals / file paths over the wire.

This bead lands the privacy fix in `internal/caps` NOW so the future discovery
write-back inherits a code, never free text. Scope is `internal/caps` + one new
contract/allowlist test. NO collector wiring, NO proto field, NO call to
`UpsertDiscoveredCapabilities` — those belong to the gated feature.

## Design decision

- Add `type ReasonCode string` with a fixed, exported, enumerable vocabulary in
  `internal/caps/caps.go`. `AllReasonCodes()` returns the closed set so a test can
  iterate it.
- Change `Status.Reason` (the field the discovery write-back serializes — the
  cross-boundary field) from `string` to `ReasonCode`.
- Keep human-readable diagnostics in a NEW collector-local `Status.Detail string`
  field, documented as NEVER crossing the wire. This preserves the operator-useful
  info (version number, role list, log dest) for local logs without leaking it.
- The store (`discovered_capability.go`) is the future serializer; it persists
  `status.Reason` (now a `ReasonCode`, encoded as text by pgx). DO NOT modify it.

### Vocabulary (covers every return path in probes.go)

| Code | Sites |
|---|---|
| `PROBE_ERROR` | every `err.Error()` site (40,78,124,156,177,184,214,230) |
| `NOT_INSTALLED` | extension missing (64) |
| `INSTALLED` | extension present (59) |
| `PARSE_ERROR` | `%q raw` server_version_num parse (86) |
| `VERSION_BELOW_BASELINE` | server_version_num < 120000 (93) |
| `VERSION_OK` | server_version_num >= baseline (99) |
| `NOT_PRELOADED` | auto_explain not in shared_preload_libraries (221) |
| `DISABLED` | auto_explain log_min_duration=-1 (238) |
| `NO_ROLE` | role lacks pg_monitor/super (135) |
| `ROLE_OK` | role has pg_monitor/super (135) |
| `LOG_REACHABLE` | log dest reachable (200) |
| `LOG_UNREACHABLE` | log dest = bare stderr (200) |
| `INTERNAL` | Discover completeness fallback (caps.go:129) |

## Tasks (TDD, red first)

1. **RED — pure unit tests** in `internal/caps/reasoncode_test.go` (no Postgres):
   - Field-allowlist reflection test on `caps.Status`: every field is exactly one
     of `Available bool`, `Reason caps.ReasonCode`, `Detail string`; `Reason` is
     kind `ReasonCode`. Fails if a new free-text field becomes the cross-boundary
     field, or if `Reason` reverts to `string`.
   - `AllReasonCodes()` is non-empty, has no duplicates, and contains the codes the
     probes use.
   - Poisoned-error test: feed `ErrToCode(errors.New("SECRET_LITERAL_email='alice@example.com'"))`
     and assert the returned `ReasonCode` contains NONE of the poisoned substring;
     also assert every `AllReasonCodes()` member is free of the sentinel.
   These fail to compile/pass before the type + helper exist.

2. **GREEN — define the enum + helper** in `caps.go`: `ReasonCode`, the const
   vocabulary, `AllReasonCodes()`, `ErrToCode(error) ReasonCode` (returns
   `PROBE_ERROR` for any non-nil error — the literal-stripping mapping). Update
   `Status` (Reason→ReasonCode, add Detail) + the doc comment to state the
   cross-boundary field is a closed code and Detail never crosses the wire.

3. **GREEN — map every Status in `probes.go`** to a code; move human detail to
   `Detail`. `grep -nE 'err\.Error\(\)|%q|pg_current_logfile' probes.go` must show
   no raw value on `Reason` (it lives only in `Detail`).

4. **Reconcile** `probes_test.go` (free-text substring asserts → assert on
   `Reason` code and/or `Detail`) and `caps_test.go:45,47` (Reason "1.10" →
   a ReasonCode; keep detail check via Detail).

## Verification

```
go build ./...
go test ./internal/caps/... ./internal/proto/...
grep -nE 'err\.Error\(\)|%q|pg_current_logfile' internal/caps/probes.go   # no Reason interpolation
```
