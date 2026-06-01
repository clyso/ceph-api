
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
