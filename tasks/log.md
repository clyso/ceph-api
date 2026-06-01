
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
