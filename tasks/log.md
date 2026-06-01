
## Investigator issues for POST /api/pool

- `api/crush_rule.proto:16` ships `enum PoolType { replication = 0; erasure = 1; }`.
  The zero-value name `replication` does NOT match the Ceph mon wire string
  `replicated` required by `osd pool create`. anatomy.md §"enums" says value
  names must equal the dashboard wire strings exactly and protojson parses an
  enum by value name — so this existing enum is already wire-wrong for any pool
  endpoint, and naively reusing it for pool create would silently produce the
  wrong `pool_type`. Flagged in §11; the enum likely needs a fix in its own
  right (the crush_rule create handler currently compares `req.PoolType ==
  pb.PoolType_erasure`, so the bug is latent there too).
- openapi.yaml POST /api/pool documents the `RBDPool.create` subclass override
  (`{pool: rbd-mirror}`) rather than the public `Pool.create` signature — a
  concrete instance of the ceph-src skill's "swagger body can be wrong"
  warning. Worth keeping that warning prominent for future pool sub-endpoints.

## Orchestrator note for POST /api/pool

- 1 implement pass + 1 fix pass. Final full-gate green.
- M1 (mechanical reviewer) was a **false positive**: the reviewer diffed
  against `main` (whole branch) instead of the local/uncommitted diff, so
  it flagged already-committed marshaler + http.yaml changes from prior
  ports as drive-by scope creep. Boss verified via `git diff HEAD` that the
  pool commit only adds standard new-service registration lines. Tooling
  tension: the mechanical-reviewer prompt says "review the local diff" but
  the agent compared against the merge-base. Worth tightening the reviewer
  prompt to pin the exact diff command (`git diff HEAD` + untracked) so it
  doesn't sweep in prior-commit changes on a multi-commit session branch.
- Investigator §Requirements modeled `flags` as `string` and the ratio
  kwargs as `string`; the deep reviewer (D1/D2) caught that the dashboard
  frontend sends `flags` as a JSON array and ratios as JSON numbers, which
  protojson would silently drop. Investigator looked at the controller
  signature but not the frontend wire shape — a recurring gap worth a note
  in the investigator prompt for `**kwargs`-style bodies.
- PARTIAL port: `configuration`/`rbd_mirroring` (librbd/mgr paths) and the
  async-task progress polling are out of scope; the former return
  ErrNotImplemented, the latter is a documented status-class/body divergence.

## Orchestrator note for GET /api/pool

- 1 implement pass + 1 fix pass. Both gates green.
- Investigator correctly classified this as `reconstruct` (zero mon
  commands; rebuilt from `osd dump` + `osd crush dump`). Modeling each pool
  as `google.protobuf.Struct` (60+ drifting keys) worked cleanly with
  `response_body` for the bare-array unwrap.
- Deep reviewer was strong here: caught a real `-nan` sanitizer-regex crash
  (D1), redundant inf-handling indirection (D2), nondeterministic
  `application_metadata` map-iteration order (D3, dup of mechanical M1), and
  two undocumented float/enum divergences (D4 type/crush_rule passthrough vs
  dashboard KeyError→500; D5 nan→"NaN"). All fixed/documented, no api_diff
  needed.
- The mechanical reviewer was explicitly told to diff `HEAD`+untracked (not
  `main`) to avoid the POST /api/pool M1 false-positive; no scope-creep
  false positive this time. Confirms the prior log note's fix suggestion.
- PARTIAL: `stats=true` (mgr time-series) returns ErrNotImplemented; the
  default no-stats list path is fully ported.
- Recurring: the harness emits stale compile-error diagnostics
  (pb.ListPools* undefined, flags/ratio type mismatches) right after proto
  edits, before `make proto` regen settles — `make gate` is green. Not a
  real failure; boss verifies via the gate, not the diagnostics.
