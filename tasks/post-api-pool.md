# Port ceph dashboard endpoint POST /api/pool

## Requirements

### 1. Summary
Create a new Ceph pool. Maps to dashboard `Pool.create`
(`third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:183-196`,
`@APIRouter('/pool', Scope.POOL)` at line 95). The handler issues an
`osd pool create` plus a series of follow-up `osd pool set` /
`osd pool set-quota` / `osd pool application enable` commands derived
from the request body.

### 2. Data source
`forwards-command` (a fixed sequence of mon commands, no new stateful
component). The dashboard's `create` is a thin orchestration over
`CephService.send_command('mon', ...)` calls — verified against
`ceph.audit.log` (§7). No mgr-map reconstruction, no cache/poller.

Two pieces of the Python are intentionally **NOT** ported (document as
divergences, see §11), neither of which is a hard-stop:
- `RbdConfiguration(pool).set_configuration(configuration)` — the
  `configuration` body key drives RBD pool config-key writes; out of
  scope for this endpoint (RBD is a separate resource).
- `_set_mirroring_mode(rbd_mirroring, pool)` — the `rbd_mirroring` body
  key toggles rbd-mirror pool mode; out of scope (rbd-mirroring resource).
- `@pool_task(...)` + `_wait_for_pgs` — the dashboard wraps create in an
  async TaskManager job that polls PG counts and returns 202 with a task
  envelope. We run the commands synchronously and return 201 (see §6/§11).

### 3. Scope & permission
First statement:
```go
if err := user.HasPermissions(ctx, user.ScopePool, user.PermCreate); err != nil {
    return nil, err
}
```
Derivation: class decorator `@APIRouter('/pool', Scope.POOL)`
(`controllers/pool.py:95`) → `user.ScopePool`
(`pkg/user/system_roles.go:54`). Method `create` is a `RESTController`
standard verb with no explicit `@*Permission`, so the POST→create
fallback applies (`permissions.md` "Default permission" table) →
`user.PermCreate` (`pkg/user/system_roles.go:80`). Single scope, no
gotchas.

### 4. Accept version
`application/vnd.ceph.api.v1.0+json`
(`openapi.yaml:10226`, the `'201'` response content type). Wrong/missing
Accept → 415.

### 5. Request
**The openapi body is wrong** — `openapi.yaml:10212-10222` documents only
`{ "pool": {default: "rbd-mirror"} }`, which is the `RBDPool.create`
subclass override (`controllers/pool.py:374-375`), not the public
`Pool.create` signature. Use the controller signature
(`controllers/pool.py:185-187`) plus the live-curl-confirmed flat
`**kwargs` keys.

All keys are **body** (POST JSON, no path/query params). Content-Type
`application/json`.

Named (typed) fields:
- `pool` **body** — string, required. Pool name.
- `pg_num` **body** — integer (`int32` is sufficient; PG counts are small
  positive ints). Required. Dashboard casts to int and uses it for BOTH
  `pg_num` and `pgp_num` in the create command.
- `pool_type` **body** — string enum `replicated|erasure`. Required.
- `erasure_code_profile` **body** — string, optional. Omit when empty
  (dashboard maps falsy → None → dropped, see §7).
- `flags` **body** — array of string, optional. Only the literal
  `"ec_overwrites"` is acted on → triggers
  `osd pool set var=allow_ec_overwrites val=true`
  (`controllers/pool.py:209-211`). Other flag values are ignored.
- `rule_name` **body** — string, optional. Forwarded as the `rule` arg of
  `osd pool create`. Omit when None.
- `application_metadata` **body** — array of string, optional. On create
  (`update_existing=False`) each entry → one
  `osd pool application enable ... app=<x> yes_i_really_mean_it=true`
  (`controllers/pool.py:212-225`).
- `configuration` **body** — object, optional. RBD config (NOT ported, §11).
- `rbd_mirroring` **body** — bool/string, optional. (NOT ported, §11).

**Flat open-key `**kwargs` (CRITICAL wire hazard).** Beyond the named
fields above, the controller forwards **arbitrary additional top-level
keys** through `**kwargs` (`controllers/pool.py:187,192` →
`_set_pool_values` → `_set_pool_keys`). Two are popped specially:
- `quota_max_objects` **body** — integer, optional →
  `osd pool set-quota field=max_objects val=<str>`.
- `quota_max_bytes` **body** — integer (`int64`, byte counts exceed 2^31),
  optional → `osd pool set-quota field=max_bytes val=<str>`.

Every **other** key becomes `osd pool set pool=<pool> var=<key>
val=str(value)` (`controllers/pool.py:233-243`), one command per key, in
request order. `pg_num` among kwargs additionally sets `pgp_num`
(`controllers/pool.py:244-245`); a `pool` key would trigger a rename
(not relevant to create — create always supplies its own `pool`).

Common kwargs the live create-pool dialog sends (each `var` is a valid
`osd pool set` choice, `MonCommands.h:1169-1171`):
`size`, `min_size`, `pg_autoscale_mode`, `compression_mode`,
`compression_algorithm`, `compression_required_ratio`,
`compression_min_blob_size`, `compression_max_blob_size`,
`target_size_bytes`, `target_size_ratio`, `pg_num_min`, `pg_num_max`,
`bulk`, `crush_rule`, `fast_read`, `target_max_bytes`,
`target_max_objects`, `eio`, `read_ratio`.

Per the `proto-grpc-gateway` skill, this flat open-key body **must NOT**
be modeled as a nested proto `map<string,string>` reached via one body
field — grpc-gateway silently drops flat top-level keys that have no
matching field. Model it as a `google.protobuf.Struct` request body (so
all flat keys survive and the handler iterates them), OR enumerate every
expected key as a typed top-level field. Struct is the faithful choice
because the key set is open. The handler then: pulls the named fields
(`pool`, `pg_num`, `pool_type`, `erasure_code_profile`, `flags`,
`rule_name`, `application_metadata`, `quota_max_*`), and treats the
remaining top-level keys as the `var/val` kwargs.

### 6. Response
Live dashboard returns **HTTP 202** with the Task envelope
`{"name": "pool/create", "metadata": {"pool_name": "<pool>"}}` because of
`@pool_task` (verified via curl, §7). We do **not** reimplement the async
TaskManager. Faithful synchronous behavior: run all commands, return an
empty/`{}` body (`google.protobuf.Empty`). grpc-gateway emits **HTTP 200**
for an `Empty` response (no custom forward-response option sets 201), so
ceph-api returns 200, not 201.

Status-class note for parity: dashboard emits 202, we emit 200 — both are
2xx success, so the status-class check passes. The body divergence (task
envelope keys `name`/`metadata` vs our empty body) is recorded as two
narrow `api_diff.yaml` per-field ignores; do not try to fake a task queue.

Error response (verified, §7): on a mon command failure
`@handle_send_command_error('pool')`
(`services/exception.py`) returns **HTTP 400** with body
`{"detail": "<msg>", "code": "<errno>", "component": "pool"}`. Map mon
errors to `types.ErrInvalidArg` (→ 400) where appropriate; e.g. enabling
`ec_overwrites` on a replicated pool returns
`[errno -22] ec overwrites can only be enabled for an erasure coded pool`.

### 7. Underlying mechanism
Verified live against `ceph.audit.log` (commands carry an auto-added
`"format": "json"`; the dashboard drops `None` kwargs and forwards `""`).

Exact dispatched sequence for
`{"pool":"testpool1","pg_num":8,"pool_type":"replicated","rule_name":"replicated_rule","application_metadata":["rbd"],"pg_autoscale_mode":"on","quota_max_bytes":1073741824,"quota_max_objects":1000}`:
```
{"prefix":"osd pool create","pool":"testpool1","pg_num":8,"pgp_num":8,"pool_type":"replicated","rule":"replicated_rule"}
{"prefix":"osd pool application enable","pool":"testpool1","app":"rbd","yes_i_really_mean_it":true}
{"prefix":"osd pool set-quota","pool":"testpool1","field":"max_objects","val":"1000"}
{"prefix":"osd pool set-quota","pool":"testpool1","field":"max_bytes","val":"1073741824"}
{"prefix":"osd pool set","pool":"testpool1","var":"pg_autoscale_mode","val":"on"}
```
Order within the handler (`controllers/pool.py:189-196` →
`_set_pool_values:205-231`):
1. `osd pool create` — always. `pg_num`/`pgp_num` both = request `pg_num`;
   `pool_type`; `rule`=`rule_name` and `erasure_code_profile`=ecp only if
   non-None (omitted otherwise — confirmed: absent from audit when None).
   (`MonCommands.h:1127-1143`.)
2. If `flags` contains `ec_overwrites`:
   `osd pool set var=allow_ec_overwrites val=true`.
3. For each `application_metadata` entry:
   `osd pool application enable pool=<p> app=<x> yes_i_really_mean_it=true`
   (`MonCommands.h:1187-1191`).
4. Quotas (only when value present, i.e. key was in body):
   `osd pool set-quota pool=<p> field=max_objects|max_bytes val=str(v)`
   (`MonCommands.h:1178-1181`). `val` is a **string**.
5. Remaining kwargs (request order): `osd pool set pool=<p> var=<key>
   val=str(value)` (`MonCommands.h:1169-1176`); `val` is a string. A
   `pg_num` kwarg also emits `pgp_num`.

All commands go to `mon` via `rados.Svc.ExecMon`. `val` args are always
strings (the dashboard does `str(value)`); the implementer must
stringify ints/bools the same way (e.g. quota `1000` → `"1000"`,
`pg_autoscale_mode "on"` stays `"on"`).

### 8. gRPC service decision
**Create a new service `Pool`** in a new `api/pool.proto`. No pool proto
or handler exists yet (`api/` has auth, cluster, crush_rule, status,
users only). First rpc: `rpc CreatePool (CreatePoolRequest) returns
(google.protobuf.Empty)`. Request body should be a
`google.protobuf.Struct` (or typed fields + a Struct/`map` for the open
kwargs — see §5 hazard). Register in `pkg/app/start.go`.

### 9. Closest in-repo analogue
`pkg/api/crush_rule_api_handlers.go` — `CrushRuleAPI.CreateRule`
(`forwards-command`: same `HasPermissions` first-statement guard, builds
a `map[string]interface{}` command with a `prefix` and conditionally
adds optional keys, then `ExecMon`). Tests:
`test/crush_rule_api_test.go` (e2e) and `test/crush_rule_parity_test.go`
(parity). Proto template: `api/crush_rule.proto` (service + Create rpc +
`CreateRuleRequest`). This handler issues a **single** command; pool
create issues a **sequence**, so loop the analogue's
build-map+ExecMon pattern per command.

### 10. Relevant src files
- `third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:95` —
  `@APIRouter('/pool', Scope.POOL)` (scope source).
- `.../controllers/pool.py:183-196` — `create` method (orchestration).
- `.../controllers/pool.py:205-253` — `_set_pool_values`,
  `_set_pool_keys`, `_set_quotas` (kwargs → command mapping).
- `.../controllers/pool.py:91-92` — `pool_task` (async Task wrapper, the
  reason for 202).
- `.../dashboard/openapi.yaml:10212-10246` — POST /api/pool route
  (Accept version + responses; body is the wrong subclass shape).
- `third_party/ceph/src/mon/MonCommands.h:1127-1143` — `osd pool create`.
- `.../MonCommands.h:1169-1176` — `osd pool set`.
- `.../MonCommands.h:1178-1181` — `osd pool set-quota`.
- `.../MonCommands.h:1187-1191` — `osd pool application enable`.
- `.../dashboard/services/exception.py` — `handle_send_command_error`
  (400 `{detail,code,component}` shape).

### 11. Open decisions (boss)
- **Async task vs sync (status 202 → 200).** Dashboard returns 202 + task
  envelope and polls PGs via TaskManager; we run synchronously and return
  200 (grpc-gateway default for `Empty`). Documented body divergence (both
  2xx). Recommend NOT
  reimplementing TaskManager (would be the "new stateful component" a
  hard-stop warns about, and the goal allows dropping the dashboard's own
  machinery). Decision: implement synchronous, absorb 202/201 in parity
  per `test/parity/README.md`.
- **`configuration` (RBD pool config) NOT ported** — out of scope for the
  pool endpoint (RBD resource). If a request includes it, either ignore
  or return `types.ErrNotImplemented`. Recommend: ignore silently for
  parity unless the dialog requires it (the kwargs path won't touch it).
- **`rbd_mirroring` NOT ported** — out of scope (rbd-mirroring resource).
  Same handling as `configuration`.
- **Open flat-kwargs body** — must be a `Struct` body (or full typed
  field enumeration), never a nested `map` field, or grpc-gateway drops
  the flat keys (§5). Implementer must not mis-model this.

## Review

### Mechanical
No failures found.

### Deep
- [x] D1: Fixed. Added `mapMonError` which inspects the go-ceph error's `ErrorCode()` and maps `-EINVAL`→`ErrInvalidArg`, `-ENOENT`→`ErrNotFound`, `-EEXIST`→`ErrAlreadyExists` (other errnos pass through to Internal). This returns the semantically-correct sentinel per the status-class policy (a rejected-arg mon error is genuinely a 400, not a server bug). Unit-tested (`Test_mapMonError`) and the e2e ec_overwrites case now asserts `InvalidArgument`. (`pkg/api/pool_api_handlers.go`)
- [x] D2: Fixed. Added a comment on the command loop documenting the intentional non-atomic, dashboard-matching behavior (create then partial config; no rollback). (`pkg/api/pool_api_handlers.go`)
- [x] D3: Fixed. Kwarg keys are now collected and `sort.Strings`-sorted before emitting `osd pool set` commands, giving a deterministic sequence. (Request order is unrecoverable — `structpb.Struct` does not preserve client JSON key order — so sorted is the best achievable determinism; vars are independent.) (`pkg/api/pool_api_handlers.go`)
- [x] D4: Fixed. Added `break` after the first `ec_overwrites` match, matching the dashboard's single membership test. (`pkg/api/pool_api_handlers.go`)
- [x] D5: Behavior left as-is (faithful: dashboard's `int(pg_num)` has no lower bound; adding one would diverge). Added `Test_buildCreatePoolCommands_pgNumZeroAccepted` to document and lock the accepted-zero boundary. (`pkg/api/pool_api_handlers_test.go`)
- [x] D6: Fixed. Added `Test_buildCreatePoolCommands_genericKwargs` (asserts `bulk:true`→`"True"`, `size:3`→`"3"`, sorted order) and `Test_buildCreatePoolCommands_quotaOnly`. (`pkg/api/pool_api_handlers_test.go`)
- [x] D7: Fixed. Corrected §6, §11 and the `api_diff.yaml` reason to state 200 (grpc-gateway's default for an `Empty` response), not 201. (`tasks/post-api-pool.md`, `test/parity/api_diff.yaml`)

### API-diff
- [x] A1: Withdrawn by api-diff reviewer on re-review — pushback verified correct against recorder.go/diff.go. Not applicable as directed — the narrowing breaks the parity test. The proposed `$.name`/`$.metadata` per-field ignores are matched only inside `Compare`, but `Compare` is never reached for this endpoint: ours returns `{}` (effectively empty) and the dashboard returns a non-empty task envelope, so the parity framework's **body-presence guard** (`recorder.go:426-439`, non-editable framework) fires a "body presence diverges" error and `continue`s *before* `Compare` runs. Only a root `$` ignore suppresses that presence check (`recorder.go:420 hasRootIgnore`). Verified empirically: `isEffectivelyEmpty(dash)=false`, `isEffectivelyEmpty(ours)=true`. This is exactly the "response shape is known divergent / empty-vs-nonempty" case the README reserves the root `$` marker for. I kept `path: $` but rewrote its reason to (a) correct 201→200 per D7 and (b) explain why per-field narrowing is impossible here. (`test/parity/api_diff.yaml`)
