# Anatomy of a ported endpoint

How one dashboard REST endpoint maps onto every layer of ceph-api. Trace
`crush_rule` (proto + handler + tests) as the worked example. Line
numbers drift — treat them as starting points, not contracts; confirm
against current source.

## File inventory for one resource

| Layer | File |
|---|---|
| Proto | `api/<svc>.proto` |
| HTTP map | `api/http.yaml` |
| Generated | `api/gen/grpc/go/*.pb.go`, `*_grpc.pb.go`, `*.pb.gw.go`; `api/openapi/ceph-api.swagger.json` |
| Handler | `pkg/api/<svc>_api_handlers.go` |
| Scopes/perms | `pkg/user/system_roles.go`; `user.HasPermissions` in `pkg/user/service.go` |
| Error sentinels | `pkg/types/errors.go` |
| Error→gRPC mapping | `convertApiError` / `ErrorInterceptor` in `pkg/api/grpc_server.go` |
| New-service registration | `pkg/api/grpc_server.go`, `pkg/api/grpc_http_gateway.go`, `pkg/app/start.go` |
| rados primitives | `pkg/rados/service.go` (`ExecMon`, `ExecMonWithInputBuff`, `ExecMgr`) |
| E2E test | `test/<svc>_api_test.go` |
| Parity test | `test/<svc>_parity_test.go` |
| Dashboard source | `third_party/ceph/src/pybind/mgr/dashboard/controllers/<res>.py` |

## Proto — `api/<svc>.proto`

- `package ceph;` + `option go_package = ".../api/ceph;pb";`. The package
  prefix `ceph.` is what `http.yaml` selectors reference.
- `google.protobuf.Empty` for void request or response (import
  `google/protobuf/empty.proto`).
- snake_case proto fields → Go PascalCase; the **HTTP wire form keeps
  snake_case** (gateway mux sets `UseProtoNames: true`).
- proto3 `optional` for nullable/omittable scalars → Go pointer
  (`*int32`, `*string`); the handler nil-checks them.
- enums: value names must equal the dashboard wire strings **exactly**
  (protojson parses an enum from its value name), so relax the
  lowercase-style convention to match the wire — e.g.
  `enum PoolType { replicated = 0; erasure = 1; }`, not `replication`. A
  proto3 enum's zero value can't be told apart from "field absent"; when
  the field is required and you must detect absence, make it `optional`
  (→ pointer, nil = absent) so the handler can reject a missing value.
- `repeated` for lists.
- **Types:** `google.protobuf.Timestamp` for time (never unix-seconds
  ints); `int64` when the upstream C++ type is 64-bit (verify in
  `third_party/ceph/`); otherwise `int32` matching the dashboard schema.
  The parity matcher coerces the wire-shape differences — see
  [parity README](../../test/parity/README.md).

## HTTP mapping — `api/http.yaml`

grpc-gateway `grpc_api_configuration` (external YAML, not in-proto
annotations). One block per rpc:

```yaml
- selector: ceph.CrushRule.ListRules
  get: /api/crush_rule
  response_body: "rules"        # unwrap a single field so the body is the bare value/array
- selector: ceph.CrushRule.GetRule
  get: /api/crush_rule/{name}   # {name} binds request field `name`
- selector: ceph.CrushRule.CreateRule
  post: /api/crush_rule
  body: "*"                     # whole JSON body → request message
- selector: ceph.CrushRule.DeleteRule
  delete: /api/crush_rule/{name}
```

- `selector` = `<protoPackage>.<Service>.<Rpc>`.
- Path `{x}` binds request field `x`.
- `body: "*"` maps the whole JSON body onto the request (POST/PUT).
- Any request field not in the path and not covered by `body` becomes a
  query param.
- `response_body: "field"` unwraps one response field so the HTTP body
  matches the dashboard's bare array/object instead of `{"field": ...}`.

## Codegen

`make proto` (runs `go tool buf generate` from `api/`). Generates the
`.pb.go` / `_grpc.pb.go` / `.pb.gw.go` stubs and the merged openapi
spec. The grpc server iface is `pb.<Svc>Server`;
`require_unimplemented_servers=false`, so handler structs need not embed
`Unimplemented<Svc>Server`.

## Handler — `pkg/api/<svc>_api_handlers.go`

- Constructor returns the generated iface, impl struct is unexported:
  `func New<Svc>API(radosSvc *rados.Svc) pb.<Svc>Server`; struct holds
  `*rados.Svc`.
- Method signature matches the generated `pb.<Svc>Server` iface.
- **First statement: permission check** — `user.HasPermissions(...)`, see
  [permissions.md](./permissions.md).
- Validate inputs → wrap a shared sentinel:
  `fmt.Errorf("%w: ...", types.ErrInvalidArg)`.
- Build a mon/mgr command as `map[string]interface{}` with `"prefix"`
  (the Ceph command literal) + args + `"format": "json"`, `json.Marshal`,
  pass the string to `radosSvc.ExecMon` / `ExecMonWithInputBuff`
  (stdin buffer) / `ExecMgr`. Add `optional` proto fields only when
  non-nil.
- Parse: `json.Unmarshal` into a local struct whose fields are the proto
  types, or directly into the proto message.
- Not-found → `return nil, types.ErrNotFound` (don't hand-map to gRPC
  codes; `ErrorInterceptor` does that centrally via `errors.Is`). Return
  the sentinel whose semantics are correct for ceph-api; when that yields
  a different HTTP **status class** than the dashboard, handle it per the
  status-class policy in [parity README](../../test/parity/README.md), not
  by degrading the code.
- Non-empty `cmdStatus` from `ExecMon` is logged, not failed — check the
  response JSON for the real error.
- A slice built by iterating a Go map has nondeterministic order, and the
  parity matcher compares arrays positionally — sort it, matching the
  dashboard's emission order (don't just sort alphabetically if the
  dashboard preserves a different order).
- Ceph's JSON formatter can emit bare `inf`/`-inf`/`nan` tokens that Go's
  `encoding/json` rejects — see the ceph-src skill's quirks list; sanitize
  before unmarshalling numeric-heavy mon output.
- Extract non-obvious pure transforms (int→string maps, id→name lookups,
  flag-string parsing, byte sanitizing) into standalone functions with no
  rados/ctx dependency, table-tested in `make gate` — see §Testing layers.

## New gRPC service registration

A **new rpc on an existing service** needs only proto + `http.yaml` +
handler. A **brand-new service** additionally touches three files:

1. `pkg/api/grpc_server.go` — add a `pb.<Svc>Server` param to
   `NewGrpcServer` and a `pb.Register<Svc>Server(srv, impl)` call.
2. `pkg/api/grpc_http_gateway.go` — inside `GRPCGateway`, call
   `pb.Register<Svc>HandlerFromEndpoint(ctx, mux, serverAddress, opts)`.
3. `pkg/app/start.go` — construct `api.New<Svc>API(radosSvc)` and pass it
   into `NewGrpcServer(...)`.

## cgo / !cgo pair

Composing JSON commands for the **existing** rados primitives needs no
cgo/!cgo edits — both `production_conn.go` (cgo) and `rados_mock.go`
(!cgo) already back `ExecMon`/`ExecMgr`. You only touch the paired files
when adding a **new RADOS primitive / connection method**. `mock-data/`
is offline-dev convenience, **not contract** — endpoint correctness is
proven by `make e2e-test` against real Ceph, so updating mock-data is not
required for a port.

## E2E test — `test/<svc>_api_test.go`

- `//go:build cgo` header (all real-RADOS tests carry it).
- `TestMain` (`test/main_test.go`) re-execs in Docker on `-tid` or runs
  `runSetup`, which boots the full app in-process against
  `testenv.NewCephEnv`, dials gRPC, and authenticates a bootstrap admin
  → `admConn`.
- Use the generated client `pb.New<Svc>Client(admConn)` + shared
  `tstCtx`, `r := require.New(t)`. Prefer a script-like flow
  (create→get→list→delete→get-404) in one `Test_*`, with cleanup. Assert
  error sentinels by their `String()` substring (`"NotFound"`,
  `"InvalidArgument"`).
- **Coverage gates run in `runSetup`** (`test/setup_cgo_test.go`):
  `AssertGRPCMethodsRouted` fails if any rpc lacks an `http.yaml` route;
  `AssertRoutesCovered` fails if any route lacks a parity test. So a port
  is incomplete (build red) until both the http mapping and the parity
  test exist.

## Testing layers

Test at the **lowest layer** that can catch the bug; reach for Docker
e2e/parity only for what the cheaper layers can't cover:

- **Pure logic** (validation, field mapping, serialization transforms,
  byte sanitizing — no rados/ctx/network) → idiomatic Go **table tests**
  in `pkg/<pkg>/*_test.go`, no cgo, runs in `make gate`. Ground each
  expected value in §Requirements / ceph-src (never in "what my handler
  returns") — a table whose answers come from the same author's mental
  model just re-encodes the blind spot.
- **rados-touching handler flow** → `//go:build cgo` e2e test (§E2E test),
  Docker + real Ceph.
- **Wire shape vs the dashboard** → parity test
  ([parity README](../../test/parity/README.md)).

The ordered build procedure lives in the implementer agent prompt; this
file is the per-layer reference it points back to.

## E2E flow completeness (resource group)

Endpoints are CRUD groups per resource. The e2e for a resource is **one
script-like flow** that must exercise the **maximal realistic interaction
across every endpoint of the resource ported so far** — not an isolated
call per endpoint. Each endpoint type, once it exists in the group,
**enables** cases on its siblings; when you add an endpoint you must
**extend the existing flow** with the newly-enabled cases, not just append
a standalone test. Two structural gaps (flow-not-extended,
create-only-when-siblings-exist) are blocking mechanical-review failures;
the rest of the matrix is reviewed case-by-case. Not optional either way.

The cases each endpoint type enables (✱ = no existing in-repo example, so
assert the **actual** observed behavior rather than copying a precedent):

- **CREATE present:**
  - happy-path create → `NoError`
  - missing required field → `InvalidArgument`
  - ✱ invalid enum / out-of-range value → error.
  - ✱ re-create when it already exists → assert what the backend actually
    does (idempotent `NoError`, or `AlreadyExists`).
  - if a created setting is **not yet observable** through a ported
    GET/LIST (e.g. only CREATE exists), assert it landed through an
    **independent channel**: `cephEnv.Exec(ctx, cmd)` runs a `ceph` CLI
    command in the container and returns its output, e.g.
    `out, _ := cephEnv.Exec(tstCtx, []string{"ceph","osd","pool","get",name,"size","-f","json"})`
    then `r.Contains(out, ...)`. Don't treat the create's own 2xx as proof.
    This CLI read-back is a **temporary workaround**, not a permanent
    assertion — tag the exact line `// TODO(crud-readback): replace with
    <Get/List> once ported` so it's greppable and gets migrated (see the
    Cross-cutting replacement rule).
- **GET-by-id present (with CREATE):**
  - get BEFORE create / nonexistent id → `NotFound`
    (`Test_GetNonExistingRule`).
  - get AFTER create → entity present **and the fields the create set are
    verified** (users `Test_Users_CRUD` checks every field).
  - get AFTER update → the change landed (if UPDATE present).
  - get AFTER delete → `NotFound` (if DELETE present) — crush_rule does this.
- **LIST present (with CREATE):**
  - list AFTER create → contains the new entity; where cheap, assert
    `count == baseline+1` (cluster `Test_ClusterUsers`).
  - list AFTER delete → absent / count back to baseline (if DELETE present).
- **UPDATE/SET present:**
  - update → GET/LIST-after verifies the change; if it mutates shared
    cluster state, restore it in `t.Cleanup` (cluster status pattern).
- **DELETE present — the trigger to revisit the whole flow:**
  - delete → `NoError`; THEN add get-after-delete→`NotFound` and
    list-after-delete→absent to the GET/LIST steps above.
  - ✱ double-delete → assert actual behavior (`NotFound` vs idempotent).

Cross-cutting:
- **Replace the CLI workaround when its gRPC read-back arrives.** When you
  port a GET/LIST that exposes a field a `// TODO(crud-readback)`
  `cephEnv.Exec` assert was checking, **delete that CLI read-back and assert
  via the new gRPC call** — the CLI channel exists only until the gRPC one
  does. Grep the resource's e2e for `TODO(crud-readback)` whenever you add
  an endpoint to the group. Leaving the CLI workaround behind once the
  gRPC read-back is available is a flow-not-extended failure (check 7).
- isolate with `t.Cleanup` using `context.Background()` so cleanup runs
  even if the test ctx is cancelled (users/cluster pattern).
- **HTTP-wire fidelity:** the e2e drives the **gRPC client**, which
  **bypasses the grpc-gateway** — so a request-body-shape bug (flat keys
  silently dropped by `DiscardUnknown`, see the `proto-grpc-gateway`
  skill) is **invisible** to e2e. The parity test is the only layer on the
  real HTTP wire: for a body with many keys, the parity case must send the
  dashboard's **real (flat) body**, and a GET/LIST parity (or independent
  read-back) must confirm the values actually landed.
