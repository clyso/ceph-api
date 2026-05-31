
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
