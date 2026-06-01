# Port ceph dashboard endpoint POST /api/pool

## Requirements

### 1. Summary
Creates an OSD pool. Maps to the dashboard `Pool.create` RESTController
method (`@APIRouter('/pool', Scope.POOL)`,
`third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:97,183-196`).
It is **not a single command** — it fans out into a fixed sequence of
mon commands (create + application-enable + quotas + per-key `osd pool
set`).

### 2. Data source
`forwards-command` (multi-command). Every effect of this endpoint is a
mon command we issue directly; no dashboard-internal state or mgr-map
reconstruction is involved. The handler reproduces the controller's
command sequence (see §7). Not a hard-stop: the `_wait_for_pgs` task
progress-polling is cosmetic (drives the async-task progress bar only)
and is NOT reproduced — see §11.

### 3. Scope & permission
First statement:
```go
if err := user.HasPermissions(ctx, user.ScopePool, user.PermCreate); err != nil {
    return nil, err
}
```
Derivation: class decorator `@APIRouter('/pool', Scope.POOL)` (`pool.py:95`)
→ `Scope.POOL == "pool"` → `user.ScopePool` (`pkg/user/system_roles.go:54`).
`create` is the standard `RESTController` POST method with no explicit
`@*Permission` decorator → POST verb-default `create` → `user.PermCreate`
(per `permissions.md` verb-fallback table). No multi-scope, no
dashboard-settings gotcha.

### 4. Accept version
`application/vnd.ceph.api.v1.0+json`
(`openapi.yaml` POST `/api/pool` → `responses.'201'.content`, line ~10226).
Wrong/missing → 415.

### 5. Request
**The openapi requestBody is WRONG** — it documents the `RBDPool.create`
subclass override (`{pool: "rbd-mirror"}`, `pool.py:374-377`), not the
public `Pool.create` signature. Use the controller source
(`pool.py:185-187`) + the live curl below as truth.

Body is `application/json`, `body: "*"`. The controller signature is
`create(self, pool, pg_num, pool_type, erasure_code_profile=None,
flags=None, application_metadata=None, rule_name=None, configuration=None,
rbd_mirroring=None, **kwargs)`. The `**kwargs` is a **flat open-key body**
— arbitrary top-level keys (`pg_autoscale_mode`, `size`, `compression_*`,
`quota_max_bytes`, `quota_max_objects`, `target_size_*`, …) forwarded
verbatim as `osd pool set` / `set-quota` args.

Named fields (all **body**):

| key | proto type | notes |
|---|---|---|
| `pool` | `string` | **required**. The pool name. |
| `pg_num` | `int32` | **required**. Dashboard does `int(pg_num)`; sent as JSON number to mon (`CephInt`). Used for both `pg_num` and `pgp_num`. |
| `pool_type` | `string` | **required**. Wire value `"replicated"` or `"erasure"`. See §11 — do **not** reuse the existing `PoolType` enum, whose zero value is misspelled `replication`. |
| `erasure_code_profile` | `optional string` | EC profile name; only meaningful for `pool_type=erasure`. |
| `flags` | `optional string` | The only inspected value is the substring `ec_overwrites` (`pool.py:209`) → fires `osd pool set allow_ec_overwrites true`. |
| `application_metadata` | `repeated string` | App names (`rbd`/`rgw`/`cephfs`). On create, every entry → `osd pool application enable`. |
| `rule_name` | `optional string` | CRUSH rule; forwarded as `rule` to `osd pool create`. Omitted (key absent) when null. |
| `configuration` | RBD config object | optional; see §11 (librbd path, out of scope for a first port). |
| `rbd_mirroring` | `optional bool`/string | optional; see §11. |

**Flat `**kwargs` keys** the create dialog commonly sends (each becomes a
top-level proto field OR a `google.protobuf.Struct` body — NEVER a nested
`map`, or the gateway's `DiscardUnknown` silently drops them and the pool
is created with defaults at HTTP 202):

| kwarg key | becomes mon command |
|---|---|
| `pg_autoscale_mode` (`string`: `on`/`off`/`warn`) | `osd pool set var=pg_autoscale_mode val=...` |
| `size` (`int`) | `osd pool set var=size val=...` |
| `quota_max_bytes` (`int64`) | `osd pool set-quota field=max_bytes val=...` |
| `quota_max_objects` (`int64`) | `osd pool set-quota field=max_objects val=...` |
| `compression_mode` (`string`) | `osd pool set var=compression_mode val=...` |
| `compression_algorithm` (`string`) | `osd pool set var=compression_algorithm val=...` |
| `compression_required_ratio`, `compression_min_blob_size`, `compression_max_blob_size`, `target_size_bytes`, `target_size_ratio`, `pg_num_min`, `pg_num_max`, … | generic `osd pool set var=<key> val=<str(value)>` |

Note `quota_max_*` are `int64` (byte/object counts can exceed 2^31).
mon `osd pool set`/`set-quota` `val` is `CephString`, so every kwarg value
is stringified (`str(value)`) before sending.

**Live request that produced §7 (HTTP 202):**
```json
{"pool":"testpool1","pg_num":8,"pool_type":"replicated",
 "rule_name":"replicated_rule","application_metadata":["rbd"],
 "pg_autoscale_mode":"on","size":1,"configuration":null}
```

### 6. Response
HTTP **202 Accepted** (dashboard runs `create` as a background `Task`,
`@pool_task('create', ...)`, `pool.py:183`). Body is the **task descriptor**,
not the pool:
```json
{"name": "pool/create", "metadata": {"pool_name": "testpool1"}}
```
(verified live). On synchronous completion within `wait_for` the dashboard
returns 201 with an empty object; under load it returns 202 with the task
body. ceph-api has no async-task framework, so the handler runs the
commands synchronously and returns `google.protobuf.Empty` (`{}`). The
parity status-class divergence (202/201 vs ceph-api's 200) is handled per
`test/parity/README.md` status-class policy — do NOT build a task queue.
Error statuses: 400 (`handle_send_command_error('pool')`, e.g. pool
exists / bad EC profile), 401, 403, 500.

### 7. Underlying mechanism
All `mon` commands, `"format":"json"`, verified against
`ceph.audit.log`. The dashboard drops `None` kwargs (omits the key) but
forwards `""` verbatim — mirror by adding optional fields only when
non-nil. Command ordering (from `pool.py:189-196` →
`_set_pool_values` → `_set_quotas` then `_set_pool_keys`):

1. **Create** (`pool.py:189`, `MonCommands.h:1127`):
   ```json
   {"prefix":"osd pool create","format":"json","pool":"testpool1","pg_num":8,"pgp_num":8,"pool_type":"replicated","rule":"replicated_rule"}
   ```
   - `pg_num` AND `pgp_num` both set from the single `pg_num` field.
   - `pool_type` always sent (`"replicated"`/`"erasure"`).
   - `rule` sent only when `rule_name` present.
   - For EC: `erasure_code_profile` sent instead of/alongside; verified:
     `{"prefix":"osd pool create",...,"pool_type":"erasure","erasure_code_profile":"default"}`.
     The dashboard maps falsy `erasure_code_profile` → `None` (omit),
     `pool.py:188`.
2. **ec_overwrites** — only if `flags` contains `ec_overwrites`
   (`pool.py:209`): `{"prefix":"osd pool set","pool":...,"var":"allow_ec_overwrites","val":"true"}`.
3. **application_metadata** — on create, for each app
   (`pool.py:215`, `MonCommands.h:1187`):
   ```json
   {"prefix":"osd pool application enable","format":"json","pool":"testpool1","app":"rbd","yes_i_really_mean_it":true}
   ```
4. **quotas** (`_set_quotas`, `pool.py:252`, `MonCommands.h:1178`) — for
   each of `quota_max_objects`→`max_objects`, `quota_max_bytes`→`max_bytes`
   that is non-None:
   ```json
   {"prefix":"osd pool set-quota","format":"json","pool":"testpool2","field":"max_objects","val":"1000"}
   ```
5. **remaining kwargs** (`_set_pool_keys`, `pool.py:235`,
   `MonCommands.h:1169`) — for each leftover kwarg key:
   ```json
   {"prefix":"osd pool set","format":"json","pool":"testpool1","var":"pg_autoscale_mode","val":"on"}
   ```
   - When the key is `pg_num`, the dashboard ALSO sets `pgp_num` to the
     same value (`pool.py:244-245`).
   - A kwarg key `pool` (rename) is NOT applicable on create (no existing
     pool to rename) — ignore for this endpoint.

Live full sequences captured (audit log): replicated w/ autoscale+size →
create, application enable, set pg_autoscale_mode, set size. With
quota+compression → create, set-quota max_objects, set-quota max_bytes,
set compression_mode, set compression_algorithm. EC → create(erasure),
application enable.

### 8. gRPC service decision
**Create a new service `Pool`** in a new `api/pool.proto` (package
`ceph`, `go_package .../api/ceph;pb`). No existing service fits — pool
endpoints (list/get/create/delete/configuration) are their own resource
group. This is a brand-new service, so it also touches
`pkg/api/grpc_server.go`, `pkg/api/grpc_http_gateway.go`, and
`pkg/app/start.go` (see anatomy.md §"New gRPC service registration").
RPC: `rpc CreatePool (CreatePoolRequest) returns (google.protobuf.Empty)`;
`http.yaml` `post: /api/pool`, `body: "*"`.

### 9. Closest in-repo analogue
`pkg/api/crush_rule_api_handlers.go` → `CreateRule`
(`forwards-command` create that builds a `map[string]interface{}` with
`prefix`, adds optional fields conditionally, branches on pool type,
calls `radosSvc.ExecMon`). Same data-source class. Its proto
(`api/crush_rule.proto`), e2e (`test/crush_rule_api_test.go`), and parity
(`test/crush_rule_parity_test.go`) are the starting templates. This
endpoint differs by issuing a **sequence** of `ExecMon` calls rather than
one, and by carrying a flat `**kwargs` body.

### 10. Relevant src files
- `third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:95` —
  `@APIRouter('/pool', Scope.POOL)` (scope).
- `.../controllers/pool.py:183-196` — `create` method + command sequence entry.
- `.../controllers/pool.py:205-231` — `_set_pool_values` (flags/app/quota/keys orchestration).
- `.../controllers/pool.py:233-247` — `_set_pool_keys` (`osd pool set` per kwarg; pg_num→pgp_num).
- `.../controllers/pool.py:249-253` — `_set_quotas` (`osd pool set-quota`).
- `.../controllers/pool.py:374-377` — `RBDPool.create` (the override openapi wrongly documents).
- `.../services/ceph_service.py:335-373` — `send_command` (drops `None` kwargs, keeps `""`).
- `.../services/rbd.py:259-265` — `RbdConfiguration.set_configuration` (the `configuration` path; librbd).
- `third_party/ceph/src/mon/MonCommands.h:1127` — `osd pool create` arg schema.
- `third_party/ceph/src/mon/MonCommands.h:1169` — `osd pool set`.
- `third_party/ceph/src/mon/MonCommands.h:1178` — `osd pool set-quota`.
- `third_party/ceph/src/mon/MonCommands.h:1187` — `osd pool application enable`.
- `third_party/ceph/src/pybind/mgr/dashboard/openapi.yaml:~10212` — POST `/api/pool` (note: body schema wrong).

### 11. Open decisions (boss)
- **Flat `**kwargs` body modeling (decision, not hedge):** model the
  common dashboard keys (`pg_autoscale_mode`, `size`, `quota_max_bytes`,
  `quota_max_objects`, `compression_mode`, `compression_algorithm`, …) as
  **typed top-level proto fields**, OR make the whole request body a
  `google.protobuf.Struct`. Do NOT use a nested `map<string,string>`:
  flat sibling keys won't bind into it and `DiscardUnknown` drops them
  silently → pool created with defaults at 200 (proto-grpc-gateway skill).
  Recommended: typed fields for the named args + the common set-keys,
  since the dialog's key set is well-bounded. Boss to confirm whether to
  also accept arbitrary unknown set-keys (→ `Struct`) or cap to the
  enumerated list.
- **`PoolType` enum reuse hazard:** the existing
  `api/crush_rule.proto:16` enum is `{ replication = 0; erasure = 1; }`
  — zero value misspelled `replication`, which does NOT equal the mon
  wire string `replicated`. Pool create REQUIRES `pool_type` to serialize
  as exactly `replicated`/`erasure`. Use a `string pool_type` field (or a
  new correctly-spelled enum) and validate against
  `{replicated,erasure}`; do not reuse the misspelled enum. Boss to
  decide enum-vs-string (string is simplest and matches the wire).
- **Async task / `_wait_for_pgs` not reproduced:** the dashboard returns
  202 and polls pg progress (`pool.py:276-305`). ceph-api has no task
  queue; the handler runs the commands synchronously and returns
  `Empty`/200. Documented status-class divergence (parity policy), not a
  hard-stop. No progress reporting.
- **`configuration` (RBD pool config) out of scope for first port:** when
  the body carries a non-null `configuration` object, the dashboard calls
  `RbdConfiguration(pool).set_configuration` which uses **librbd** (`rbd
  config pool set`), not a mon command. Recommend ignoring `configuration`
  for the initial port (most pool creates omit it) and returning
  `ErrNotImplemented` if it is supplied, OR a follow-up task. Boss to
  decide. Same for `rbd_mirroring` (calls `RbdMirroringPoolMode`, mgr
  rbd_support). Both are optional and absent in the common create dialog.
- **Best-effort partial failure:** the command sequence is non-atomic —
  if `osd pool set` fails after `osd pool create` succeeds, the pool
  exists but is partially configured (same as the dashboard). No rollback;
  return the first error. Acceptable, matches upstream.

## Review

### Mechanical (M*)
- [ ] M1: FALSE POSITIVE. None of the cited changes are in this commit. The pool working-tree diff (vs HEAD) touches `api/http.yaml` (only the new `ceph.Pool.CreatePool` route block), `pkg/api/grpc_http_gateway.go` (only `RegisterPoolHandlerFromEndpoint`), `pkg/api/grpc_server.go` (only the `poolAPI pb.PoolServer` param + `RegisterPoolServer`), and `pkg/app/start.go` (only `NewPoolAPI` + the new ctor arg) — all standard new-service registration. The `response_body:"status"` on CreateUser/UpdateUser, the `GetCephPgDump` route, the `UseProtoNames`/`EmitUnpopulated`/`DiscardUnknown` marshaler config, and the `protoadapt.MessageV1` migration are all already present in HEAD (committed before this work); `git show HEAD:` confirms each. The reviewer diffed against `main` (the whole branch) rather than the pool commit.

### Deep (D*)
- [x] D1: Fixed. Verified pool-form.component.ts:801-804 sends `flags` as `['ec_overwrites']` (an array). Changed proto `flags` to `repeated string` and the handler now uses `poolFlagsHaveECOverwrites([]string)` membership check.
- [x] D2: Fixed. Verified the form `ratio` control (pool-form.component.ts:135, validators min(0)/max(1) at :551-554) serializes `compression_required_ratio` as a JSON number; the controller stringifies via `str(value)` (pool.py:235). Changed both `compression_required_ratio` and `target_size_ratio` to `optional double`; handler stringifies via `formatPoolFloat` (`strconv.FormatFloat(f,'g',-1,64)` → "0.8", matching Python `str`). Note: `target_size_ratio` is not actually emitted by this dashboard form, but modeling it as `double` is correct for any client sending the standard numeric ratio.
- [x] D3: Fixed. The re-create now asserts `r.NoError(err)` to document mon-side idempotency (verified green in e2e).
- [x] D4: Fixed. Added `Test_Pool_Create_PartialFailureLeavesPool`: an invalid `pg_autoscale_mode` makes the follow-up `osd pool set` fail after `osd pool create` succeeds; the test asserts the call errors yet the pool exists (`ceph osd pool ls`), pinning the non-atomic contract.
- [x] D5: Fixed. `pg_num` is the named field, never returned by `poolSetKeys`, so the `kv.key == "pg_num"` mirroring branch was dead — removed it from `poolCreateCommands`. Replaced the unreachable-branch test with `Test_poolCreateCommands_ratioStringified`, which exercises the D2 ratio-stringification path.
- [x] D6: Fixed. After each backend creates, the parity test now reads back the exercised fields (`size`, `pg_autoscale_mode`, `application_metadata`) via CLI and asserts they landed on both backends, so a dropped kwarg surfaces. The dashboard runs create as an async task, so the readback polls (up to 30s) for the setting to settle before asserting. (The matcher already runs in `DoRecord`; the `$` ignore covers only the body envelope — see A1.)
- [x] D7: Fixed. Reworded the proto comment to say a "present configuration object (including an empty {})" yields ErrNotImplemented, matching the handler's `!= nil` guard.

### API-diff (A*)
- [x] A1: Fixed. The `$` entry stays (irreducible body-presence divergence). Verified recorder.go:407 compares status class `/100` before and independently of the `$` ignore (hasRootIgnore at :419). Rewrote the reason to cite only the body presence/content divergence and to state explicitly that status class is equal (both 2xx) and compared independently of the ignore; dropped the "2xx status sub-code differ irreducibly" framing.
