
## Investigator issues for POST /api/pool

- The dashboard `openapi.yaml:10212-10222` POST /api/pool requestBody
  schema is actively misleading: it documents only `{"pool": "rbd-mirror"}`
  (the `RBDPool.create` override / PoolUi default), not the real public
  `Pool.create(pool, pg_num, pool_type, ...)` signature
  (`controllers/pool.py:185`). The task prompt already warns the openapi
  body may be incomplete, but here it is wrong, not just incomplete — an
  implementer trusting it would ship a broken endpoint. Captured the real
  shape from controller source + live curl.
- `api/crush_rule.proto:16` defines `enum PoolType { replication = 0; erasure = 1; }`
  but the dashboard/mon string for replicated pools is `"replicated"`, not
  `"replication"`. Reusing this enum requires an explicit enum→string map
  in the handler; the naming mismatch is a latent foot-gun.
- Missing required body fields return HTTP 500 (Python TypeError) on the
  live dashboard, not a 400. This collides with anatomy.md's guidance to
  return `ErrInvalidArg` (→400). Parity may flag 400-vs-500; noted in the
  task file for the boss to decide.

## Implementer issues for POST /api/pool

- §Requirements said for `osd pool create` the `rule` arg is "always
  present (null when no rule_name)". This is wrong: the dashboard's
  `CephService.send_command` strips all `None` kwargs
  (`services/ceph_service.py:360` — `{k: v for k, v in kwargs.items() if
  v is not None}`), so `rule=None` and `erasure_code_profile=None` are
  never sent. The dashboard does forward an empty string (`rule:""`)
  verbatim when `rule_name=""` (verified live via audit log — POST with
  `rule_name:""` dispatches `"rule": ""` and returns 201; mon treats ""
  as the default rule). Fixed by omitting `rule` only when the proto
  field is nil and passing an empty string through
  (`pkg/api/pool_api_handlers.go`). (The earlier EINVAL-on-explicit-null
  claim in this log was unverified and is retracted.)
- §Requirements recommended reusing `crush_rule.proto`'s `PoolType` enum
  (values `replication`/`erasure`) and mapping enum→mon string in the
  handler. That breaks parity: the parity test sends one byte-identical
  body to both backends with `"pool_type":"replicated"`, which is not a
  valid value name for that enum, so protojson can't parse it. Used a
  plain `string pool_type` field instead to match the dashboard wire form
  exactly.

## Investigator issues for GET /api/pool

- Mechanism mismatch with the porting model: the task prompt and
  anatomy.md assume each endpoint maps to a mon/mgr command shape to
  forward. GET /api/pool issues NO mon command (audit log empty) — the
  dashboard reads mgr's in-process `mgr.get('osd_map')` cache. ceph-api
  has no such cache, so the port must reconstruct equivalent data from
  `osd dump` + `osd crush dump`. The "concrete rados commands" deliverable
  fits awkwardly here; documented the reconstruction strategy instead.
- `stats=true` is only partially reproducible. The dashboard's per-pool
  `stats[*].rate`/`rates` and `pg_status` come from mgr's historical
  counter cache (`get_updated_pool_stats`, `pg_summary`) with no
  mon-command equivalent. The pipeline has no guidance for "endpoint is in
  dashboard openapi but a sub-feature can't be faithfully reproduced from
  mon commands" — it's neither a clean skip nor a clean port. Flagged for
  the boss; suggested porting stats=false first.
- POOL_SCHEMA (controllers/pool.py:19-88) and openapi.yaml under-document
  the response: live wire has `is_stretch_pool`, `read_balance.score_type`,
  `snap_mode`, `peering_crush_bucket_*` not in the schema, and the reused
  `OsdDumpPool` proto (api/status.proto:201-296) also lacks
  `is_stretch_pool` and `read_balance.score_type`. Source+swagger alone
  would have produced a lossy response; live curl was required.

## Implementer issues for GET /api/pool

- §Requirements claimed `create_time` would auto-coerce because "parity
  coerces RFC3339 ↔ unix-seconds" (tasks/get-api-pool.md:147,215). That is
  only half-true: the dashboard returns `create_time` as a *string* in
  Ceph's RFC3339 `+0000` spelling (e.g. `2026-05-31T11:30:15.842225+0000`),
  while protojson renders the Timestamp as a *string* with `Z`. Both sides
  are strings, so the existing `coerceEqual` (which only bridged
  string↔number) did not apply and parity failed on all pools. Had to
  extend `coerceEqual` in `test/parity/diff.go` with a string↔string
  RFC3339 same-instant branch (+ `parseCephTime` handling the colon-less
  `+0000` offset) and a `diff_test.go` test. Investigator should note that
  string-vs-string timestamp formats are NOT auto-coerced by the stock
  matcher.
- §Requirements modeled the `attrs` query param as "only the whitelisted
  attrs are emitted" (tasks/get-api-pool.md:22-26). This is not faithfully
  reproducible with a typed proto response, which always emits all
  populated fields. The param is kept on `ListPoolsRequest` for surface
  compatibility but the handler returns the full object regardless of
  `attrs`. Parity only exercises the default (no-attrs) path, so it did not
  block, but the divergence is real and documented in the parity test
  header.
