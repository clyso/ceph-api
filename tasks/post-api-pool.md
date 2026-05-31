# Port ceph dashboard endpoint POST /api/pool

## Requirements

### 1. Summary
Creates a Ceph pool (replicated or erasure). Maps to the dashboard
`Pool.create` RESTController method
(`third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:183-196`).
It is a multi-command orchestration: one `osd pool create` followed by a
series of `osd pool set` / `set-quota` / `application enable|disable`
commands derived from the request body.

### 2. Data source
`forwards-command` (multi-command). Every field maps to a concrete
mon command issued via `CephService.send_command('mon', ...)`. Confirmed
against `ceph.audit.log` (see §7). There is **no** new stateful component
required: the dashboard's `_wait_for_pgs` task-progress polling
(`pool.py:276-305`) only drives the async Task progress bar and produces
no response data — we do not reproduce it (the implementer runs the
commands synchronously and returns).

Two optional fields touch non-mon-command paths (flagged in §11):
- `configuration` (rbd QoS `conf_*` options) → librbd `pool_metadata_set`,
  NOT a mon command (`services/rbd.py:259-265`, `.set` at `:206-231`).
- `rbd_mirroring` → an rbd-mirror mgr/peer command
  (`controllers/rbd_mirroring.py:511`).

### 3. Scope & permission
First statement:
```go
if err := user.HasPermissions(ctx, user.ScopePool, user.PermCreate); err != nil {
    return nil, err
}
```
Derivation: `@APIRouter('/pool', Scope.POOL)` (`pool.py:95`), `Scope.POOL
== "pool"` (`security.py:14`) → `user.ScopePool` (`pkg/user/system_roles.go:54`).
`create` is a standard `RESTController` method with no explicit
`@*Permission` decorator → POST verb fallback → `PermCreate`
(per `permissions.md` "Default permission" table). No gotcha (single scope).

### 4. Accept version
`application/vnd.ceph.api.v1.0+json` (`openapi.yaml` POST `/api/pool`,
line ~10226). Wrong/absent → 415.

### 5. Request
Whole JSON body → request message (`body: "*"`). **The openapi request
body is wrong** — it documents only `{pool: string, default rbd-mirror}`,
which is the `RBDPool` subclass override (`pool.py:374-377`), not the real
`Pool.create` signature. The true signature is `pool.py:185-187`.
Confirmed by live curl. All fields are **body**.

Required:
- `pool` **string** — pool name. (REST framework requires it; missing → 400.)
- `pg_num` **int32** — placement group count. Python does `int(pg_num)`;
  **omitting it is a 500** on the live dashboard (not 400), because
  `int(None)` throws (verified). Treat as required; reject missing with
  `ErrInvalidArg` (status-class divergence vs the dashboard's 500 — see §11).
- `pool_type` **string** — `"replicated"` | `"erasure"` (forwarded verbatim
  as the `pool_type` arg).

Optional:
- `erasure_code_profile` **string** — EC profile name. Forwarded as the
  `erasure_code_profile` create arg **only when non-empty** (Python coerces
  `"" → None`, `pool.py:188`, then `send_command` drops `None`). Omit when
  empty/nil.
- `rule_name` **string** — CRUSH rule. Forwarded as the `rule` arg of
  `osd pool create` (name change: `rule_name` → `rule`). Omit when nil.
- `flags` **string** — only `ec_overwrites` is acted on: if the string
  contains `ec_overwrites`, fire `osd pool set var=allow_ec_overwrites
  val=true` (`pool.py:209-211`). Any other flags value is ignored.
- `application_metadata` **repeated string** — app names. On create
  (`update_existing=False`) the "original" set is empty, so each entry
  fires `osd pool application enable pool=<p> app=<a>
  yes_i_really_mean_it=true` (`pool.py:212-225`). Omit ⇒ no app commands.
- `configuration` **object/map<string,scalar>** — rbd QoS options
  (`conf_*` pool metadata). NOT a mon command — see §11.
- `rbd_mirroring` **bool** (sent as string, `str_to_bool`) — enable rbd
  pool mirror mode. NOT a mon command — see §11.
- **Arbitrary extra keys (`**kwargs`)** — every other body key `K` with
  value `V` becomes `osd pool set pool=<p> var=K val=str(V)`
  (`pool.py:233-247`). Two special-cased keys inside kwargs:
  - `quota_max_objects` / `quota_max_bytes` → popped and sent as
    `osd pool set-quota field=max_objects|max_bytes val=str(V)`
    (`pool.py:228-230`, `_set_quotas` `:249-253`).
  - `pg_num` (if present in kwargs) → additionally sets `pgp_num` to the
    same value (`pool.py:244-245`). (Note: on create, `pg_num` is already a
    named param, so this kwargs branch is not normally hit on POST.)
  - `pool` (if present in kwargs) → triggers `osd pool rename`
    (`pool.py:239-247`); not used on create.

  Observed kwargs on real create dialogs: `size` (replicated size, int),
  `pg_autoscale_mode` (`on|off|warn`), `compression_mode`
  (`none|passive|aggressive|force|unset`), `compression_algorithm`,
  `compression_required_ratio`, `compression_min_blob_size`,
  `compression_max_blob_size`, `target_size_bytes`, `target_size_ratio`,
  `pg_num_min`. All allowed `var` values are the strings in the `osd pool
  set` enum (`MonCommands.h:1169-1174`).

  **Modeling note for the implementer:** a typed proto cannot capture
  arbitrary `**kwargs`. Model the known fields explicitly and add a generic
  `map<string,string> options` (or similar) for the pass-through `osd pool
  set` keys. The `val` is always `str(value)` — emit as a string. See §11.

Live request that produced the §7 commands:
```json
{"pool":"probe_pool_repl","pg_num":8,"pool_type":"replicated",
 "rule_name":"replicated_rule","application_metadata":["rbd"],
 "pg_autoscale_mode":"on","size":2,"compression_mode":"none",
 "quota_max_bytes":1073741824,"quota_max_objects":1000}
```

### 6. Response
- **202** (Operation still executing) with body
  `{"name": "pool/create", "metadata": {"pool_name": "<pool>"}}` when the
  Task does not finish within ~2s (the normal case — verified).
- **201** (Resource created) with body `null` when it finishes fast
  (verified: re-creating an existing pool returns 201/`null`, because
  `osd pool create` is idempotent and completes immediately).
- The body is a **Task descriptor**, not pool data. ceph-api has no async
  Task queue; the handler runs the commands synchronously and returns
  `google.protobuf.Empty`. The HTTP status divergence (ceph-api will emit
  a single 2xx, the dashboard alternates 201/202) is a documented
  status-class matter for parity — see `test/parity/README.md` and §11.
- Errors: `400` for a `SendCommandError` (mon command rc≠0, wrapped by
  `@handle_send_command_error('pool')`); `500` for Python-level failures
  (e.g. missing `pg_num`); `401`/`403` for auth.

### 7. Underlying mechanism
Exact ordered command sequence, confirmed against `ceph.audit.log` on
`ghcr.io/arttor/ceph-test:v19`. All carry `"format": "json"`; `None`
kwargs are dropped, `""`/`true` forwarded verbatim.

Replicated probe (`{pool, pg_num:8, replicated, rule_name, app=[rbd],
pg_autoscale_mode:on, size:2, compression_mode:none, quota_max_*}`):
```
{"prefix":"osd pool create","pool":"probe_pool_repl","pg_num":8,"pgp_num":8,"pool_type":"replicated","rule":"replicated_rule"}
{"prefix":"osd pool application enable","pool":"probe_pool_repl","app":"rbd","yes_i_really_mean_it":true}
{"prefix":"osd pool set-quota","pool":"probe_pool_repl","field":"max_objects","val":"1000"}
{"prefix":"osd pool set-quota","pool":"probe_pool_repl","field":"max_bytes","val":"1073741824"}
{"prefix":"osd pool set","pool":"probe_pool_repl","var":"pg_autoscale_mode","val":"on"}
{"prefix":"osd pool set","pool":"probe_pool_repl","var":"size","val":"2"}
{"prefix":"osd pool set","pool":"probe_pool_repl","var":"compression_mode","val":"none"}
```
Erasure probe (`{pool, pg_num:8, erasure, erasure_code_profile,
flags:"ec_overwrites", app=[rbd], configuration:{rbd_qos_bps_limit}}`):
```
{"prefix":"osd pool create","pool":"probe_pool_ec","pg_num":8,"pgp_num":8,"pool_type":"erasure","erasure_code_profile":"probeprof"}
{"prefix":"osd pool set","pool":"probe_pool_ec","var":"allow_ec_overwrites","val":"true"}
{"prefix":"osd pool application enable","pool":"probe_pool_ec","app":"rbd","yes_i_really_mean_it":true}
```
Notes:
- `pgp_num` is always set equal to `pg_num` on create (`pool.py:189-191`).
- Command emission order matters if the parity test asserts audit-log
  order: create → ec_overwrites (if flag) → application enable (per app) →
  quotas (objects then bytes) → remaining `osd pool set` kwargs (dict
  iteration order = JSON body key order).
- The EC `configuration` field produced **no** mon command (it goes to
  librbd pool metadata — §11).

Command definitions:
- `osd pool create` — `MonCommands.h:1127-1144` (args: pool, pg_num,
  pgp_num, pool_type, erasure_code_profile, rule, …; cap `osd rw`).
- `osd pool set` — `MonCommands.h:1169-1174` (pool, var, val).
- `osd pool set-quota` — `MonCommands.h:1178-1182` (pool, field
  max_objects|max_bytes, val).
- `osd pool application enable` — `MonCommands.h:1187-1192` (pool, app,
  yes_i_really_mean_it).

### 8. gRPC service decision
**Create a new service `Pool`** in a new `api/pool.proto`. There is no
existing pool proto (`api/*.proto` has auth, cluster, crush_rule, status,
users only). This is the first endpoint of the pool resource; future
`GET/PUT/DELETE /api/pool` land here too. New-service registration touches
`pkg/api/grpc_server.go`, `pkg/api/grpc_http_gateway.go`,
`pkg/app/start.go` (anatomy.md §"New gRPC service registration").

### 9. Closest in-repo analogue
`pkg/api/crush_rule_api_handlers.go` — `CreateRule` (same §Data-source
class: a POST that builds a `map[string]interface{}` command with a
`prefix` + args, `json.Marshal`, `radosSvc.ExecMon`, returns
`emptypb.Empty`; handles a `PoolType_{replicated,erasure}` enum branch
exactly like this endpoint's `pool_type`). Tests:
`test/crush_rule_api_test.go` (e2e, `//go:build cgo`, create→get→list→
delete flow) and `test/crush_rule_parity_test.go` (parity). Use these as
the starting templates; the pool create handler is a longer command
sequence but the same primitives.

### 10. Relevant src files
- `controllers/pool.py:95` — `@APIRouter('/pool', Scope.POOL)` class decl.
- `controllers/pool.py:183-196` — `create` method (the orchestration).
- `controllers/pool.py:205-231` — `_set_pool_values` (ec_overwrites, apps,
  quotas, kwargs dispatch).
- `controllers/pool.py:233-247` — `_set_pool_keys` (`osd pool set` per key,
  pgp_num mirror, rename).
- `controllers/pool.py:249-253` — `_set_quotas`.
- `controllers/pool.py:374-377` — `RBDPool.create` override (the source of
  the misleading openapi body).
- `services/ceph_service.py:335-373` — `send_command` (drops `None`,
  adds `format:json`, raises `SendCommandError` on rc≠0).
- `services/rbd.py:206-265` — `RbdConfiguration.set`/`set_configuration`
  (librbd `pool_metadata_set`, the `configuration` field).
- `security.py:14` — `Scope.POOL = "pool"`.
- `src/mon/MonCommands.h:1127,1169,1178,1187` — command schemas.
- `openapi.yaml` ~10212-10246 — POST `/api/pool` (Accept v1.0; wrong body).

### 11. Open decisions (boss) — RESOLVED / applied

**Implemented as a documented partial port.** Boss decisions applied:
1. **`configuration`** — DEFERRED. Handler returns `ErrNotImplemented`
   (→ gRPC `Unimplemented`) when `configuration` is non-empty. The librbd
   `pool_metadata_set` path is not implemented; add it with the pool
   GET/`configuration` endpoint port.
2. **`rbd_mirroring`** — DEFERRED. Handler returns `ErrNotImplemented` when
   `rbd_mirroring == true`. `false`/absent is accepted (no-op). The dashboard
   drives the mode whenever `rbd_mirroring is not None`: it sets mode `pool`
   for `true` and `disabled` for `false` (`pool.py:194-203`). We reproduce
   neither rbd-mirror command (the rbd-mirror peer/mode path is unported). For
   a faithful proxy on create we reject only `true`; `false` is a no-op because
   `disabled` is already the default on a fresh pool, so the omitted disable
   command has no observable effect at create time. If a future port wants full
   fidelity, `false` could be wired to the disable command (or rejected) along
   with the `true` path.
3. **Async Task** — run synchronously, return `google.protobuf.Empty`. No
   Task queue. Status/body divergence (dashboard 201/202 + Task descriptor
   vs our 2xx + empty body) absorbed via `test/parity/api_diff.yaml`
   (`POST /api/pool`, `path: $`, with reason). Status class is `2` on both,
   so only the body shape needed the ignore.
4. **Missing `pg_num`** — rejected with `ErrInvalidArg` (→ 400). The
   dashboard 500s here (Python `int(None)`). This systematic error-status
   wart is covered in the e2e test on our side, NOT diffed as a parity
   scenario (per the parity README status-class policy).
5. **`**kwargs` pass-through** — modeled known fields explicitly plus a
   generic `map<string,string> options`. Forwarded verbatim as
   `osd pool set var/val` (faithful proxy; mon rejects unknown vars).
   `quota_max_objects`/`quota_max_bytes` in `options` are special-cased to
   `osd pool set-quota`; `pg_num` in `options` additionally mirrors
   `pgp_num`.

**Documented partial-port divergence:** the `configuration` and
`rbd_mirroring` request fields are accepted in the proto but rejected at
runtime with `Unimplemented` when set. A dashboard client relying on either
on create will get an error from ceph-api until those paths are ported.

#### Original open decisions (for reference)
1. **`configuration` field (rbd QoS `conf_*` pool metadata).** Not a mon
   command — the dashboard uses librbd `pool_metadata_set`
   (`services/rbd.py:206-231`). go-ceph exposes
   `rbd.GetPoolMetadata`/`SetPoolMetadata`, so it is reproducible via a new
   RADOS primitive (cgo/!cgo pair), but it adds librbd surface. **Decision
   needed:** implement it now (new `rados.Svc` rbd-metadata primitive) vs.
   ship create without it and return `ErrNotImplemented` if `configuration`
   is non-empty. Recommend deferring (`configuration` is rarely set on
   create; can be added with the pool GET/`configuration` endpoint port).
2. **`rbd_mirroring` field.** Enables rbd pool mirror mode via an
   rbd-mirroring command (`controllers/rbd_mirroring.py:511`), a separate
   resource not yet ported. Same decision: defer with `ErrNotImplemented`
   when non-nil, or pull in the rbd-mirror command. Recommend deferring.
3. **Async Task vs synchronous.** The dashboard wraps create in a Task and
   returns 201/202 with a Task descriptor body. ceph-api has no Task queue;
   run synchronously and return `Empty`. This diverges the HTTP status
   (single 2xx vs 201/202) and the body (Empty vs Task JSON). Handle via the
   status-class divergence policy in `test/parity/README.md`; do not invent
   a Task system.
4. **Missing `pg_num` status class.** Dashboard returns **500**; ceph-api
   should reject with `ErrInvalidArg` → 400. Documented divergence (the
   dashboard's 500 is a Python `int(None)` crash, not intended behavior).
5. **`**kwargs` pass-through.** A typed proto can't model arbitrary keys.
   Model known fields + a generic `map<string,string> options` for the
   `osd pool set var/val` pass-through. Restrict accepted `var` keys to the
   `osd pool set` enum (`MonCommands.h:1171`) and reject unknown ones with
   `ErrInvalidArg`, or forward verbatim and let mon reject. Recommend
   forward-verbatim to stay a faithful proxy.

## Review

### Mechanical
- [x] M1: `buildCreatePoolCommands` emits remaining `options` keys in alphabetical sort order, but §7 specifies JSON body key order (dict iteration order). Test comment ack's "(sorted)" as deliberate. No parity/e2e asserts audit-log order, so no functional impact — but document the proto-map limitation rather than implying parity. (`pkg/api/pool_api_handlers.go`, dup of D1)
  - Added a comment at the sort site explaining the proto-map has no defined iteration order (body order unrecoverable), the sort is for determinism only, and it is not a parity guarantee.
- [x] M2: Parity test `Test_Parity_Pool_Create` creates `parity_pool_create` with no pre-cleanup and no `t.Cleanup` (no DELETE endpoint yet). On repeated runs the pool exists; `osd pool create` is idempotent so it should still 2xx, but the test is non-idempotent vs the crush_rule analogue which pre-deletes. (`test/pool_parity_test.go:14`)
  - No DELETE endpoint exists, so the crush_rule pre-delete pattern is not reproducible. Documented in a file-level comment that `osd pool create` idempotency makes the test robust across repeated runs (re-create over an existing pool still 2xx on both backends).

### Deep
- [x] D1: `options` kwargs emitted sorted, not body order (proto `map` has no defined order so body order is unrecoverable). Harmless for independent `osd pool set` vars, but sorting is arbitrary — add a one-line comment noting the proto-map limitation rather than silently sorting. (`pool_api_handlers.go:129-136`, same as M1)
  - Same fix as M1.
- [x] D2: `rbd_mirroring: false` should issue a disable command, not no-op. Dashboard fires `_set_mirroring_mode` whenever `rbd_mirroring is not None`; for `false` it calls `set_pool_mirror_mode(pool,'disabled')` (`pool.py:194-203`). Handler treats false/absent identically as no-op. On a fresh pool disabled is default so effect is nil — document the divergence in §Open decisions. (`pool_api_handlers.go:68-70`)
  - Documented in §Open decisions (item 2) and at the handler site: rbd_mirroring is deferred entirely; we reproduce neither the enable nor disable command. Faithful-proxy choice on create is to reject only `true`; `false` stays a no-op because `disabled` is the default on a fresh pool, so the omitted command has no effect at create time.
- [x] D3: No abort-on-partial-failure semantics — `CreatePool` runs N commands sequentially, returns on first error, leaving a partially-configured pool. Matches dashboard (no rollback there either) so faithful; note only (one comment or task line), don't change. (`pool_api_handlers.go:38-46`)
  - Behavior unchanged (faithful to the dashboard). Added a one-line comment at the command loop noting the no-rollback semantics match the dashboard.
- [x] D4: `map[string]interface{}` vs idiomatic `map[string]any`. Consistent with crush_rule analogue. Cosmetic; leave unless codebase migrating. (`pool_api_handlers.go:55,72-88`)
  - Dismissed: cosmetic and consistent with the crush_rule analogue; codebase is not migrating to `any`. Left as-is.
- [x] D5: Parity test only asserts 2xx, never compares bodies/effects (body fully ignored via `api_diff.yaml path:$`), covers only replicated happy path. Consider adding erasure+ec_overwrites and an options/quota case, or document why status-only is sufficient. (`test/pool_parity_test.go:14-31`)
  - Strengthened: the replicated case now carries size/quota/options in the body, and a new `Test_Parity_Pool_Create_Erasure` exercises erasure + ec_overwrites. (Both stay status-only because the body is intentionally ignored via api_diff.yaml; the value is exercising both code paths on both backends.)
- [x] D6: e2e `Test_CreatePool` only checks `NoError` on create, never reads back pool state (size/quota/app/pg_autoscale). A dropped `set`/`set-quota` would pass green. Add a raw `osd pool get`/`osd dump` read-back, or note the gap until pool GET lands. (`test/pool_api_test.go:13-83`)
  - Added a read-back subtest that runs `ceph osd pool get` (size, pg_autoscale_mode, compression_mode), `get-quota`, and `application get` in JSON through a new exported `CephEnv.Exec` helper, asserting the requested values were applied independently of the API under test.
- [x] D7: `pg_num` in `options` mirrors `pgp_num` (faithful to `pool.py:244-245`) but is unreachable on create; the unit test feeds contradictory `PgNum:4`+`Options[pg_num]:16`. Keep for proxy-fidelity but reconsider the misleading synthetic test case. (`pool_api_handlers.go:146-154`, `pool_api_handlers_test.go:116-131`)
  - Kept the fidelity code. Renamed the test, removed the contradictory `PgNum:4`, and added a comment that the Options `pg_num` branch is unreachable from POST /api/pool and the case is synthetic to exercise the mirror branch in isolation.
