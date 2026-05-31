# Port ceph dashboard endpoint POST /api/pool

## Requirements

### Summary
Create a new pool. Maps to `Pool.create` (a `RESTController` POST) in
`third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:185`. The
handler issues a sequence of mon commands (`osd pool create` then 0..N
follow-ups). Verified against live `ghcr.io/arttor/ceph-test:v19`.

### Scope & permission
- `@APIRouter('/pool', Scope.POOL)` (`pool.py:95`) → `user.ScopePool`.
- `create` has **no** explicit `@*Permission` decorator → RESTController
  POST verb default = create → `user.PermCreate`.
- Emit as first handler statement:
  ```go
  if err := user.HasPermissions(ctx, user.ScopePool, user.PermCreate); err != nil {
      return nil, err
  }
  ```
- Verified live: 401 (no token), 403 (read-only user).

### Required Accept version
`application/vnd.ceph.api.v1.0+json` (openapi.yaml:10226). Wrong version
→ 415 `Incorrect version: endpoint is '1.0', client requested 'X.Y'`
(verified). Content-Type of request body: `application/json`.

### Request body (POST body: "*")
The openapi POST schema (openapi.yaml:10212-10222) is WRONG/misleading —
it shows only `{"pool": "rbd-mirror"}`, which is the `RBDPool.create`
override schema, not the real public `Pool.create`. Read the controller
signature (`pool.py:185-187`) for the truth. Concrete shape:

```jsonc
{
  "pool": "testpool1",            // string, REQUIRED. pool name
  "pg_num": 8,                    // int32, REQUIRED. cast int() in py; passed as pg_num AND pgp_num
  "pool_type": "replicated",      // string, REQUIRED. "replicated" | "erasure"
  "erasure_code_profile": "default", // string, optional. only sent to mon when truthy (erasure pools)
  "rule_name": "replicated_rule", // string, optional → mon arg "rule"
  "flags": "ec_overwrites",       // string, optional. if contains "ec_overwrites" → extra "osd pool set allow_ec_overwrites true"
  "application_metadata": ["rbd"],// []string, optional. each → "osd pool application enable"
  "configuration": null,          // RBD config dict, optional (RbdConfiguration.set_configuration). Out of scope: only meaningful for rbd config keys; leave unsupported/empty for v1
  "rbd_mirroring": false,         // bool/str, optional. enables rbd pool mirror mode. Out of scope for v1
  "quota_max_bytes": 1073741824,  // int64, optional (kwargs) → "osd pool set-quota field=max_bytes val=<str>"
  "quota_max_objects": 1000,      // int64, optional (kwargs) → "osd pool set-quota field=max_objects val=<str>"
  "pg_autoscale_mode": "on",      // string, optional (kwargs) → "osd pool set var=pg_autoscale_mode"
  "compression_mode": "aggressive"// string, optional (kwargs) → "osd pool set var=compression_mode"
  // ...any other key (kwargs) → "osd pool set" var=<key> val=<str(value)>; key "pg_num" also sets "pgp_num"; key "pool" triggers "osd pool rename"
}
```
- `pool`, `pg_num`, `pool_type` are positional/required: omitting any →
  Python TypeError → **HTTP 500** (verified; not validated as 400).
  Implementer should still validate and return `ErrInvalidArg` (400) — a
  cleaner behavior; flag in api_diff if parity complains about 500 vs 400.
- `flags` is a **comma-or-substring string** in the dashboard (`'ec_overwrites' in flags`),
  not a list.
- All numeric values: `pg_num` is int32 in practice (CephInt). Quotas are
  byte/object counts → use `int64`. They are stringified before being
  sent to mon (`val=str(value)`).
- The arbitrary-kwargs passthrough (any `osd pool set` var) is the hard
  part to model in proto. Pragmatic v1: model the explicit named fields
  (pool, pg_num, pool_type, erasure_code_profile, rule_name, flags,
  application_metadata, quota_max_bytes, quota_max_objects,
  pg_autoscale_mode, compression_*) plus an optional
  `map<string,string>` for additional `osd pool set` vars if parity
  demands the generic passthrough. Confirm scope with the boss.

### Underlying mechanism (mon commands, verified via ceph.audit.log)
All are `CephService.send_command('mon', ...)` → `rados.ExecMon`. Order:

1. `osd pool create` (always):
   ```json
   {"prefix":"osd pool create","format":"json","pool":"<pool>","pg_num":<n>,"pgp_num":<n>,"pool_type":"<replicated|erasure>","rule":"<rule_name|null>","erasure_code_profile":"<ecp>|omitted-if-null"}
   ```
   - CORRECTION (verified live + ceph_service.py:360): `rule` is NOT always
     present. `CephService.send_command` drops kwargs whose value is `None`,
     so the dashboard OMITS `rule` when `rule_name` is unset, and forwards an
     empty string `rule:""` verbatim when `rule_name=""` (mon accepts "" as
     the default rule; audit log confirms `"rule": ""` for a `rule_name:""`
     POST returning 201). The handler mirrors this: omit when the proto field
     is nil, pass through when present (even empty). `erasure_code_profile`
     present only when truthy. MonCommands.h:1127.
2. For each `application_metadata` entry (new pool → all are "enable"):
   `{"prefix":"osd pool application enable","format":"json","pool":"<pool>","app":"<app>","yes_i_really_mean_it":true}` (MonCommands.h:1187).
3. If `flags` contains `ec_overwrites`:
   `{"prefix":"osd pool set","format":"json","pool":"<pool>","var":"allow_ec_overwrites","val":"true"}` (MonCommands.h:1169).
4. Quotas (only when value not None):
   `{"prefix":"osd pool set-quota","format":"json","pool":"<pool>","field":"max_objects|max_bytes","val":"<str>"}` (MonCommands.h:1178).
5. Each remaining kwarg → `{"prefix":"osd pool set","format":"json","pool":"<pool>","var":"<key>","val":"<str(value)>"}`; `pg_num` key additionally sets `pgp_num`; `pool` key → `osd pool rename`.

Order observed live for testpool2: create → application enable →
set-quota(max_objects) → set-quota(max_bytes) → set(pg_autoscale_mode) →
set(compression_mode). Then `_wait_for_pgs` (PG polling loop, dashboard
task progress only — no mon command needed for parity).

### Response shape & statuses
- The dashboard wraps `create` in `@pool_task('create', ...)` (async
  task, `wait_for=2.0`). Two possible success outcomes (both verified):
  - **201 Created**, body `null` — task finished within 2s.
  - **202 Accepted**, body `{"name":"pool/create","metadata":{"pool_name":"<pool>"}}` — still running.
  - The 201-vs-202 split is timing-dependent. ceph-api is synchronous; it
    should return **201 with empty body** on success. Flag the 202/task-body
    divergence for parity (api_diff if needed) — ours has no task queue.
- 400: operation exception (mon command error wrapped by
  `handle_send_command_error('pool')`, `pool.py:184`).
- 401 unauthenticated, 403 unauthorized (perm), 500 internal/TypeError.
- Recreating an existing pool returns 201/null (osd pool create is
  idempotent at the mon — no error). Verified.

### gRPC service decision
**Create a new service `Pool`** in a new `api/pool.proto` (package
`ceph`, `option go_package = ".../api/ceph;pb"`). No pool proto/handler
exists yet (`api/` has only auth, cluster, crush_rule, status, users).
This is a brand-new service → also register in:
- `pkg/api/grpc_server.go` (param + `pb.RegisterPoolServer`)
- `pkg/api/grpc_http_gateway.go` (`pb.RegisterPoolHandlerFromEndpoint`)
- `pkg/app/start.go` (construct `api.NewPoolAPI(radosSvc)`)
Reuse the existing `PoolType` enum already defined in
`api/crush_rule.proto:16` (values `replication=0`, `erasure=1`) — note the
dashboard sends the string `"replicated"` (not "replication"); the handler
must map proto enum → the mon string `replicated`/`erasure`.

### http.yaml block
```yaml
- selector: ceph.Pool.CreatePool
  post: /api/pool
  body: "*"
```
Return `google.protobuf.Empty` (empty body matches the 201/null success).

### Relevant src files
- `third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py` —
  `Pool.create` (:185), `_set_pool_values` (:205), `_set_pool_keys`
  (:233), `_set_quotas` (:249), `pool_task` (:91). The authoritative
  request shape & command sequence.
- `third_party/ceph/src/pybind/mgr/dashboard/openapi.yaml:10212` — POST
  route (Accept version, statuses) — body schema is unreliable, see above.
- `third_party/ceph/src/mon/MonCommands.h:1127,1169,1178,1187` — mon
  command arg specs for create/set/set-quota/application enable.
- `third_party/ceph/src/pybind/mgr/dashboard/services/exception.py:95` —
  `handle_send_command_error` (mon error → DashboardException/400).
- ceph-api refs: `api/crush_rule.proto` (proto pattern + PoolType enum),
  `api/http.yaml` (route blocks), `pkg/api/crush_rule_api_handlers.go`
  (handler/command-build pattern with `ExecMon`).

### Implementer notes
- Build each command as `map[string]interface{}` with `"format":"json"`,
  marshal, `radosSvc.ExecMon(...)`. Issue them in the order above.
- Send `pgp_num == pg_num`. Always include `rule` (null if absent) and
  `pgp_num`. Include `erasure_code_profile` only when non-empty.
- Stringify quota/set values (`val` is a string in the mon command).
- `flags` substring check for `ec_overwrites`.
- Empty success body (Empty / 201).

## Review

### Mechanical
- [x] M1: FALSE POSITIVE — the task spec was wrong, not the code. Verified
  live + `ceph_service.py:360`: the dashboard's `send_command` drops `None`
  kwargs, so it OMITS `rule` entirely when `rule_name` is unset; it does NOT
  send `rule:null`. Omitting on nil is the correct, parity-faithful behavior;
  no `api_diff.yaml` entry is needed. Corrected §Underlying mechanism step 1.
  (D4 below handles the empty-string nuance the spec also got wrong.)
- [x] M2: DISMISSED (string kept deliberately, with D1's alternative applied).
  The `PoolType` enum's value names are `replication`/`erasure`, but the
  dashboard's REST contract accepts the string `"replicated"`. protojson
  rejects `"replicated"` for that enum (verified: `invalid value for enum
  field`), so using the enum as the request field would 400 a request the
  dashboard answers 2xx — a parity regression. The enum's proto3 zero value
  (`replication`) would also make the required-field check impossible
  (omitted == "replicated"), whereas the dashboard treats `pool_type` as
  required and 500s when absent. So the field stays `string`, and D1's
  "validate against the two legal values up front" alternative is applied,
  giving the same clean-400 protection the enum was meant to provide.

### Deep
- [x] D1: validate `pool_type` against the exact two legal mon strings
  (`replicated`/`erasure`) up front, returning `ErrInvalidArg` (400) for
  typos/`"replication"` instead of an opaque mon EINVAL
  (`pkg/api/pool_api_handlers.go`). e2e covers the `"replication"` 400 case.
- [x] D2: documented at the top of `test/pool_api_test.go` why no cleanup is
  possible (no `DeletePool` RPC ported yet; fixed names + idempotent create).
- [x] D3: reworded the comment to the verified mechanism — `send_command`
  drops `None` kwargs (`ceph_service.py`), so `rule` is omitted when unset and
  an empty string is forwarded verbatim. Dropped the unverified EINVAL claim.
- [x] D4: probed live — dashboard with `rule_name:""` sends `"rule": ""` to
  the mon (audit log) and returns 201. Handler now omits `rule` only when the
  proto field is nil and passes an empty string through, matching the
  dashboard's `None`-filter exactly (`pkg/api/pool_api_handlers.go`).
- [x] D5: parity body now sends `application_metadata`, `quota_max_bytes`,
  `quota_max_objects`, `pg_autoscale_mode`, and `compression_mode`, so all
  modeled follow-up mon commands fire on both backends. Documented the
  intentionally-dropped generic `**kwargs` passthrough divergence in the
  parity test header (`test/pool_parity_test.go`). Side effect: the fuller
  body pushes the dashboard past its 2s task `wait_for`, so it now returns
  202 with a task envelope while ours returns 201/empty — the timing-dependent
  async-task divergence the spec flagged (§Response shape). Since ceph-api has
  no task queue this is irreconcilable, so it is now declared in
  `test/parity/api_diff.yaml` ("POST /api/pool", `$` root ignore) with a full
  reason.
- [x] D6: `Test_CreatePool` now reads the pool back via `Status.GetCephOsdDump`
  and asserts the quota and application_metadata follow-ups landed
  (`test/pool_api_test.go`).
