# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Ceph API is a standalone Go service that exposes REST + gRPC APIs for administrating a Ceph cluster, as an alternative to the Ceph mgr RESTful module. It connects to a Ceph cluster over RADOS (rados user, keyring, mon host) using `github.com/ceph/go-ceph`, so it can run anywhere with `mon` reachability.

## Build & run

The binary requires CGO and the Ceph client libraries (`librados`, `librbd`, `libcephfs`) on the build/host machine.

- **Build (real RADOS):** `CGO_ENABLED=1 go build ./cmd/ceph-api` — fails to link without ceph dev libs.
- **Run mock mode (no Ceph cluster needed):** `CGO_ENABLED=0 CFG_APP_CREATEADMIN=true CFG_APP_ADMINUSERNAME=admin CFG_APP_ADMINPASSWORD=yoursecretpass go run ./cmd/ceph-api/main.go`. Under `CGO_ENABLED=0`, `pkg/app/rados_conn_mock.go` and `pkg/rados/rados_mock.go` (`//go:build !cgo`) compile in instead of the real go-ceph connection. They serve canned JSON from `pkg/rados/mock-data/{mon,mon-input,mgr}/`.
- **macOS dev:** ceph dev libs aren't available natively. Use the Lima VM defined in `lima-ceph-dev.yaml` (`limactl create --name=ceph ./lima-ceph-dev.yaml && limactl start ceph`).
- **Docker:** `docker-compose up` brings up a Ceph demo container plus the API on `:9969` with admin `admin`/`yoursecretpass`.

## Make targets

The Makefile is the canonical entry point for all dev workflows. Run `make help` for a current list.

- `make check` — `go fmt` + `go vet` + unit tests. No CGO, no Docker.
- `make lint` — `golangci-lint` via `go tool`.
- `make e2e-test` — `go test ./test/... -tid`. Builds the test binary inside `test/Dockerfile` (has librados/librbd/libcephfs), then runs it; tests spawn Ceph via testcontainers using the mounted host `docker.sock`. The inner container uses host networking to reach the sibling Ceph container.
- `make gate` — `check` + `lint`.
- `make full-gate` — `gate` + `e2e-test`.
- `make ceph-ref` — populate `third_party/ceph` at the pinned commit.
- `make ceph-ref-versions` — also fetch tags listed in `CEPH_REF_EXTRA_TAGS`.
- `make proto` — regenerate gRPC stubs + OpenAPI from `api/*.proto`.

## Tests

E2E tests live in `test/` and boot the full `app.Start` in-process against a real Ceph cluster started by `test/testenv/CephEnv`. Unit tests live alongside their packages under `pkg/`.

- **Run e2e:** `make e2e-test`.
- **Run unit tests only:** `make check`.
- **Single e2e test:** `go test ./test/ -tid -run TestAuth -v`.
- **Test harness:** `test/testenv/CephEnv` starts `ghcr.io/arttor/ceph-test:v19` (multi-arch, pre-baked all-in-one cluster) on a private bridge network (static MON IP `192.168.56.7`), waits for `ceph health`, then enables `mgr dashboard` and `mgr restful` with known credentials so parity tests can hit both the dashboard API and ceph-api against the same cluster.
- **Build-tag layout in `test/`:**
  - `main_test.go` — no tag. `TestMain` + `-tid` flag dispatch.
  - `setup_cgo_test.go` — `//go:build cgo`. Declares shared vars (`tstCtx`, `admConn`, `cephEnv`, …) and `runSetup` that boots `CephEnv` + `app.Start`.
  - `setup_nocgo_test.go` — `//go:build !cgo`. Stub `runSetup` that errors with "use `-tid`".
  - Every other `*_test.go` in `test/` carries `//go:build cgo`.

`golangci-lint`, `buf`, and the protoc plugins are declared as `tool` directives in `go.mod` and invoked via `go tool <name>`. Add new Go-based tools the same way: `go get -tool <pkg>@<ver>`.

## API generation

`.proto` files in `api/` are the **single source of truth** for both gRPC and REST APIs. To change the API:

1. Edit a `.proto` in `api/` (or add a new one for a new service).
2. Map any new RPC to its HTTP route in `api/http.yaml` (consumed by grpc-gateway).
3. Run `make proto` — emits gRPC stubs and gateway wrappers to `api/gen/grpc/go/` and OpenAPI to `api/openapi/ceph-api.swagger.json`.
4. Implement the server stub in `pkg/api/`.
5. If you added a **new service** (not just a method), register it in both `pkg/api/grpc_server.go` and `pkg/api/grpc_http_gateway.go`.

## Architecture

**Single binary, single port.** gRPC and HTTP share the same listener via `soheilhy/cmux`; the HTTP side is grpc-gateway translating REST → gRPC plus a few hand-mounted OAuth handlers. Both default to `:9969`.

**Composition root: `pkg/app/start.go`.** `app.Start` builds the dependency graph in a fixed order: tracer → RADOS connection (real or mock via build tag) → `rados.Svc` → `cephconfig.NewConfig` → API services (`api.NewClusterAPI`, `api.NewUsersAPI`, `api.NewCrushRuleAPI`, `api.NewStatusAPI`) → `user.New` (optionally creating the configured admin) → `auth.NewServer` (OAuth provider) → `api.NewGrpcServer` → `api.GRPCGateway` → `api.Serve`. To add a new gRPC service, instantiate it here and pass it to `NewGrpcServer`.

**RADOS layer (`pkg/rados/`).** `Svc` is a thin wrapper over a `RadosConnInterface` exposing three primitives: `ExecMon`, `ExecMonWithInputBuff`, `ExecMgr`. **All Ceph interactions go through these.** Higher-level packages (`pkg/api/`, `pkg/cephconfig/`, `pkg/user/`) construct JSON command payloads and parse JSON responses. The real connection is in `production_conn.go` (`//go:build cgo`); the mock in `rados_mock.go` (`//go:build !cgo`) returns randomly-picked canned responses keyed by command prefix from the embedded `mock-data/` FS.

**Auth (`pkg/auth/`).** OAuth 2.0 server built on `ory/fosite`. Token state and user/role records are persisted **in Ceph itself** via the rados layer (no external DB). Two HTTP auth surfaces coexist:
- `/api/oauth/{token,auth,revoke,introspect}` — proper OAuth 2.0 (password, refresh, etc.).
- `/api/auth` — legacy Ceph-dashboard-compatible login (no refresh token); kept for backwards compatibility.

Authentication on gRPC calls is enforced by `auth.AuthFunc` wired as a gRPC interceptor in `NewGrpcServer`.

**Users & permissions (`pkg/user/`).** Permission model mirrors upstream Ceph: each role grants a subset of `{read, create, update, delete}` over a fixed set of scopes (`pool`, `osd`, `monitor`, `rgw`, `cephfs`, `user`, etc.) defined in `pkg/user/system_roles.go`. Don't invent new scopes or permissions — extend `scopeSet`/`permissionSet` there if truly needed.

**Configuration (`pkg/config/`).** Precedence: defaults from `pkg/config/config.yaml` → `-config <path>` → `-config-override <path>` → env vars of the form `CFG_<UPPER_YAML_PATH>` (e.g. `CFG_APP_ADMINPASSWORD`). The override flag exists specifically so secrets can be mounted from a separate file (e.g. a k8s Secret) without rewriting the main config.

**Generated code.** `api/gen/grpc/go/*.pb.go`, `*_grpc.pb.go`, `*.pb.gw.go` and `api/openapi/ceph-api.swagger.json` are buf output — do not hand-edit; regenerate via `make proto` after changing protos. The repo-root `go_api_client.go` is a hand-written convenience wrapper around the generated gRPC client.

## Ceph reference (`third_party/ceph`)

The upstream Ceph repo is a git submodule at `third_party/ceph`, pinned to `v19.2.3` (squid). It is reference-only — never built or linked. Casual `git clone` does **not** populate it; run `make ceph-ref` once to fetch a slim history (`--filter=blob:none --depth=1`).

Use it to:
- Look up dashboard handler logic for new endpoints — `third_party/ceph/src/pybind/mgr/dashboard/`.
- Look up restful handler logic — `third_party/ceph/src/pybind/mgr/restful/`.
- Read Ceph CLI command definitions — `third_party/ceph/src/mon/MonCommands.h`, `src/mgr/*.cc`.
- Read upstream docs — `third_party/ceph/doc/`.

For cross-release diffs, `make ceph-ref-versions` fetches `v18.2.7` and `v20.0.0`. Then `git -C third_party/ceph diff v18.2.7..v19.2.3 -- src/pybind/mgr/dashboard/foo.py` shows the API drift.

## Conventions worth knowing

- **Build tags gate the RADOS backend.** Files in `pkg/app/`, `pkg/rados/`, and `pkg/types/` come in `cgo` / `!cgo` pairs. Under `CGO_ENABLED=1` the real go-ceph connection compiles in; under `CGO_ENABLED=0` the mock does. When adding new behavior that touches the connection, mirror it in both files.
- **Errors from Ceph come back as JSON in stdout plus a status string.** See `rados.Svc.ExecMon` — non-empty `cmdStatus` is logged but doesn't itself fail the call; check the response JSON for actual errors.
- **No DB.** All persistent state (users, OAuth clients, tokens) lives in Ceph via rados commands or the config-key store (`pkg/cephconfig/`).
- **Mock mode is offline-dev convenience, not contract.** `CGO_ENABLED=0` builds make `make check` runnable without ceph libs, but the mock JSON in `pkg/rados/mock-data/` is hand-curated and routinely lags real Ceph. Wire-format bugs are caught by `make e2e-test` against a real cluster, not by `make check`. New endpoint work must be validated against `make e2e-test`. Updating `mock-data/` for new endpoints is **not** required.
- **Tests live in two places.** Unit tests next to the code they test (`pkg/**/*_test.go`). E2E tests in `test/`. Test harness in `test/testenv/`. Anything in `test/` runs against a real Ceph started by `testenv.NewCephEnv` — no mocks.

## Comments

Default to **zero comments**. Only keep one when removing it would leave a future reader unable to derive a non-obvious invariant, workaround, or external constraint. Before finishing any edit, re-scan every comment added; if you can't justify it under one of the three tests below, delete it.

**Banned:**
- History narration (`legacy`, `previously`, `used to`, `now uses`, `added for X`).
- Internal task/session references (`tracked as #123`, `see TASKS.md 2.1`, `Phase 2.3 summary`).
- Foreign-project name-drops (`from cesto`, `matches chorus pattern`).
- Restating well-named code or paraphrasing identifiers.
- Doc comments that just restate the function signature.

**Keep if any of:**
1. The WHY is non-obvious from reading the surrounding code.
2. A future agent or human couldn't re-derive it without the comment.
3. It describes an external constraint — library contract, protocol quirk, framework artifact, regulatory requirement.

Examples that pass: `// fosite sets session.Subject via SetSubject after Authenticate`, `// grpc-gateway emits {} for Empty proto responses`, `//nolint:gosec // self-signed cert on localhost-only listener`.
