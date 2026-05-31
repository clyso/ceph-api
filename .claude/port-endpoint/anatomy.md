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

## 1. Proto — `api/<svc>.proto`

- `package ceph;` + `option go_package = ".../api/ceph;pb";`. The package
  prefix `ceph.` is what `http.yaml` selectors reference.
- `google.protobuf.Empty` for void request or response (import
  `google/protobuf/empty.proto`).
- snake_case proto fields → Go PascalCase; the **HTTP wire form keeps
  snake_case** (gateway mux sets `UseProtoNames: true`).
- proto3 `optional` for nullable/omittable scalars → Go pointer
  (`*int32`, `*string`); the handler nil-checks them.
- enums use lowercase values; the zero value is the default.
- `repeated` for lists.
- **Types:** `google.protobuf.Timestamp` for time (never unix-seconds
  ints); `int64` when the upstream C++ type is 64-bit (verify in
  `third_party/ceph/`); otherwise `int32` matching the dashboard schema.
  The parity matcher coerces the wire-shape differences — see
  [parity README](../../test/parity/README.md).

## 2. HTTP mapping — `api/http.yaml`

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

## 3. Codegen

`make proto` (runs `go tool buf generate` from `api/`). Generates the
`.pb.go` / `_grpc.pb.go` / `.pb.gw.go` stubs and the merged openapi
spec. The grpc server iface is `pb.<Svc>Server`;
`require_unimplemented_servers=false`, so handler structs need not embed
`Unimplemented<Svc>Server`.

## 4. Handler — `pkg/api/<svc>_api_handlers.go`

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
  codes; `ErrorInterceptor` does that centrally via `errors.Is`).
- Non-empty `cmdStatus` from `ExecMon` is logged, not failed — check the
  response JSON for the real error.

## 5. New gRPC service registration

A **new rpc on an existing service** needs only proto + `http.yaml` +
handler. A **brand-new service** additionally touches three files:

1. `pkg/api/grpc_server.go` — add a `pb.<Svc>Server` param to
   `NewGrpcServer` and a `pb.Register<Svc>Server(srv, impl)` call.
2. `pkg/api/grpc_http_gateway.go` — inside `GRPCGateway`, call
   `pb.Register<Svc>HandlerFromEndpoint(ctx, mux, serverAddress, opts)`.
3. `pkg/app/start.go` — construct `api.New<Svc>API(radosSvc)` and pass it
   into `NewGrpcServer(...)`.

## 6. cgo / !cgo pair

Composing JSON commands for the **existing** rados primitives needs no
cgo/!cgo edits — both `production_conn.go` (cgo) and `rados_mock.go`
(!cgo) already back `ExecMon`/`ExecMgr`. You only touch the paired files
when adding a **new RADOS primitive / connection method**. `mock-data/`
is offline-dev convenience, **not contract** — endpoint correctness is
proven by `make e2e-test` against real Ceph, so updating mock-data is not
required for a port.

## 7. E2E test — `test/<svc>_api_test.go`

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

## Porting checklist (derived)

1. Read `third_party/ceph/.../controllers/<res>.py` for routes,
   `@APIRouter(Scope.X)`, per-method permission, the mon/mgr command, and
   `openapi.yaml` for the per-route `Accept` version. Use the `ceph-src`
   skill — never training data.
2. Define/extend `api/<res>.proto` (Empty for void; optional→pointer;
   int64/Timestamp where upstream is 64-bit/time).
3. Add `http.yaml` blocks (path params, `body:"*"`, `response_body` for
   list-unwrap).
4. `make proto`.
5. Implement `pkg/api/<res>_api_handlers.go`: `HasPermissions` first →
   validate → build JSON cmd → `ExecMon`/`ExecMgr` → unmarshal → sentinel
   on not-found.
6. New service only: register in `grpc_server.go`, `grpc_http_gateway.go`,
   `start.go`.
7. Add the `//go:build cgo` e2e test and the parity test (both required
   by the coverage gates). See [parity README](../../test/parity/README.md).
8. `make gate` then `make full-gate`.
