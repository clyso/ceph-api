# CLAUDE.md

ceph-api is a Go service exposing REST + gRPC APIs to administrate a Ceph
cluster, as an alternative to the Ceph mgr RESTful module. It connects to
Ceph over RADOS via `github.com/ceph/go-ceph`, so it runs anywhere with
mon reachability.

## The porting workflow

Use these files when porting new endpoint from ceph mgr module:
- `.claude/port-endpoint/anatomy.md` — how an endpoint maps onto
  every layer + the porting checklist.
- `.claude/port-endpoint/permissions.md` — dashboard → Go
  permission mapping.
- `test/parity/README.md` — fix/implement parity test from `make full-gate`
- The **`ceph-src` skill** — the only source of Ceph facts. **Never
  derive Ceph behavior from training data — read the pinned submodule.**

## Build, test, gates

`make help` lists targets. Key ones:
- `make proto` — regenerate gRPC stubs + OpenAPI after any `.proto` /
  `http.yaml` edit (runs `buf generate`).
- `make gate` — fmt + vet + unit tests + lint. Fast, no Ceph
  (`CGO_ENABLED=0`).
- `make full-gate` — `gate` + `make e2e-test` (the e2e suite in a Docker
  container with librados/librbd/libcephfs, against real Ceph from
  `testenv.CephEnv`). The full pre-commit check; needs only Go + Docker.

The real-RADOS build needs `CGO_ENABLED=1` + Ceph dev libs. For portable development, run tests in a container with all libs against a real ceph testcontainer with `-tid`.
Only Go and Docker are needed for development and build/run/test against real ceph on ANY machine.
**API source of truth:** `api/*.proto` + `api/http.yaml`. **Composition root:** `pkg/app/start.go`.

Tests: unit tests sit next to code (`pkg/**/*_test.go`) - simple checks with no CGO; e2e tests in
`test/` boot the full `app.Start` in-process against real Ceph. Use
`r := require.New(t)`; table-driven for validation; script-like `t.Run`
flows for integration. `TestMain` cleanup: `defer` does not run with
`os.Exit` — use `exitCode := m.Run(); cleanup(); os.Exit(exitCode)`.

## Build-tag pair: cgo vs !cgo

Files in `pkg/app/`, `pkg/rados/`, `pkg/types/` come in `cgo` / `!cgo`
pairs. `CGO_ENABLED=1` compiles the real go-ceph connection
(`production_conn.go`); `CGO_ENABLED=0` uses the mock (`rados_mock.go`)
returning canned JSON from `pkg/rados/mock-data/`. Mirror new RADOS-touch
behavior in both. `mock-data/` is offline-dev convenience, **not
contract** — endpoint correctness is proven by `make e2e-test`, so
updating it is not required. The same pattern is in `test/`:
`setup_cgo_test.go` / `setup_nocgo_test.go`, and every `test/*_test.go`
carries `//go:build cgo`.

## Architecture in one paragraph

Single binary, single port (`:9969` default). gRPC and HTTP share one
listener via `soheilhy/cmux`; the HTTP side is grpc-gateway translating
REST → gRPC plus hand-mounted OAuth handlers. All Ceph interactions go
through `rados.Svc`'s three primitives — `ExecMon`,
`ExecMonWithInputBuff`, `ExecMgr` — and higher-level packages (`pkg/api/`,
`pkg/cephconfig/`, `pkg/user/`) construct JSON commands and parse JSON
responses. Auth is OAuth 2.0 (`ory/fosite`); persistent state lives in
Ceph via rados commands or the config-key store — no external DB.
Permission model mirrors upstream Ceph: a fixed set of scopes/permissions
in `pkg/user/system_roles.go`. **Don't invent new scopes.** Authorization
is per-handler (`user.HasPermissions` as the first statement) — there is
no central guard, so a missing call = an unprotected endpoint.

## Errors

Use shared sentinels from `pkg/types/errors.go` (`ErrNotFound`,
`ErrAlreadyExists`, `ErrInvalidArg`, `ErrAccessDenied`,
`ErrUnauthenticated`, `ErrInternal`, `ErrNotImplemented`,
`ErrInvalidConfig`). Don't make package-local errors when a shared one
exists. Compare with `errors.Is`, never `==`. Handlers return sentinels;
the central `ErrorInterceptor` maps them to gRPC codes. RADOS-specific
sentinels are in the `cgo`/`!cgo` pair `pkg/types/ceph_errors.go` /
`ceph_errors_mock.go`. Ceph errors come back as JSON in stdout plus a
status string — non-empty `cmdStatus` from `ExecMon` is logged but does
not fail the call; check the response JSON for the real error.

## Parity vs API quality

Parity tests (`test/parity/`) keep ceph-api a drop-in replacement for the
dashboard's REST surface. They do **not** dictate the gRPC type system.
When the two collide, keep the well-typed proto and absorb the wire-shape
divergence in the matcher (`coerceEqual` in `test/parity/diff.go`) — not
by regressing the proto, not by piling per-endpoint ignores in
`api_diff.yaml`. The matcher already coerces, for free: RFC3339 ↔
unix-seconds (within `timestampSkewTolerance`), int64-as-string ↔ number,
and `null` ↔ absent. So use `google.protobuf.Timestamp` for time, `int64`
where upstream C++ is 64-bit, and `json_name` for camelCase fields. New
shape-class divergences go in `coerceEqual` with a `diff_test.go` test;
reserve `api_diff.yaml` for genuinely endpoint-specific divergences. See
`test/parity/README.md`.

## Code style

- **Private by default.** Unexported unless used outside the package.
- **Interface naming:** capitalized interface (`Service`), lowercase impl
  (`service`), constructor returns the interface.
- **Minimize scope:** declare variables right before first use.

## Comments

Default to **zero comments**. Keep one only when removing it would leave a
reader unable to derive a non-obvious invariant, workaround, or external
constraint. Re-scan every comment you add; if it fails all three keep-tests
below, delete it.

**Banned:** history narration (`legacy`, `previously`, `now uses`);
internal task/session references (`tracked as #123`, `Phase 2.3`);
foreign-project name-drops; restating well-named code; doc comments that
restate the signature.

**Keep if any of:** (1) the WHY is non-obvious from the surrounding code;
(2) a future reader couldn't re-derive it; (3) it states an external
constraint (library contract, protocol quirk, framework artifact).

## Repo is self-contained

Everything needed to work here lives in the repo: the Ceph source
(`third_party/ceph` submodule), skills (`.claude/skills/`), and agents
(`.claude/agents/`). Skills, agents, and docs must **not** reference paths
outside this repo — anyone can clone and work.

**Do not use the agent memory system in this project.** Memory lives
per-machine in each user's home directory, so it's absent for other
contributors. Persist anything
durable to repo files instead: pipeline state in `tasks/`, conventions
and agent instructions in `.claude/` and this file. Don't read from or
write to memory here.
