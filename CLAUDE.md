# CLAUDE.md

ceph-api is a Go service exposing REST + gRPC APIs for administrating a
Ceph cluster, as an alternative to the Ceph mgr RESTful module. It
connects to a Ceph cluster over RADOS via `github.com/ceph/go-ceph`, so
it can run anywhere with mon reachability.

## Where to find what

- **Build, run, test, lint:** `make help` is canonical — don't memorise
  targets. Real-RADOS build needs `CGO_ENABLED=1` plus Ceph dev libs
  (`librados`, `librbd`, `libcephfs`); macOS dev uses the Lima VM in
  `lima-ceph-dev.yaml`. Mock mode (`CGO_ENABLED=0`) needs no Ceph.
- **Upstream Ceph source + docs** (dashboard, `restful`, mon/mgr command
  tables, RADOS surface, architecture docs, cross-release diffs, live
  probing recipes): `.claude/skills/ceph-src/` —
  [`SKILL.md`](./.claude/skills/ceph-src/SKILL.md) for source paths,
  [`verify.md`](./.claude/skills/ceph-src/verify.md) for probing a
  running dashboard. **Never derive Ceph behavior from training data
  — read the submodule.**
- **API source of truth:** `api/*.proto` + `api/http.yaml`. Regenerate
  stubs and OpenAPI via `make proto`.
- **Composition root:** `pkg/app/start.go` builds the dependency graph.

## Build-tag pair: cgo vs !cgo

Files in `pkg/app/`, `pkg/rados/`, and `pkg/types/` come in `cgo` /
`!cgo` pairs. Under `CGO_ENABLED=1` the real go-ceph connection
(`production_conn.go`) compiles in; under `CGO_ENABLED=0` the mock
(`rados_mock.go`) returns canned JSON from `pkg/rados/mock-data/`. When
adding behavior that touches the RADOS connection, mirror it in both
files.

`mock-data/` is offline-dev convenience, not contract. New endpoint
work is validated by `make e2e-test` against a real cluster started by
`test/testenv/CephEnv`; updating `mock-data/` is **not** required.

The same pair pattern shows up in `test/`: `main_test.go` (no tag),
`setup_cgo_test.go` / `setup_nocgo_test.go`, and every other
`*_test.go` in `test/` carries `//go:build cgo`.

## Architecture in one paragraph

Single binary, single port (`:9969` default). gRPC and HTTP share one
listener via `soheilhy/cmux`; the HTTP side is grpc-gateway translating
REST → gRPC plus hand-mounted OAuth handlers. All Ceph interactions go
through `rados.Svc`'s three primitives: `ExecMon`,
`ExecMonWithInputBuff`, `ExecMgr` — higher-level packages
(`pkg/api/`, `pkg/cephconfig/`, `pkg/user/`) construct JSON commands
and parse JSON responses. Auth is OAuth 2.0 (`ory/fosite`); persistent
state (users, OAuth clients, tokens) lives in Ceph via rados commands
or the config-key store — no external DB. Permission model mirrors
upstream Ceph: a fixed set of scopes/permissions in
`pkg/user/system_roles.go`. Don't invent new scopes.

## Code style

- **Private by default.** Funcs, structs, vars, and consts are
  unexported unless used outside the package.
- **Interface naming:** capitalized interface (`Service`), lowercase
  impl (`service`), constructor returns the interface
  (`func NewService(...) (Service, error)`).
- **Minimize variable scope:** declare right before first use.

## Tests

Unit tests live next to the code they test (`pkg/**/*_test.go`); e2e
tests live in `test/` and boot the full `app.Start` in-process against
the real Ceph started by `testenv.CephEnv`. `make e2e-test` runs the
e2e suite inside a Docker container that has librados/librbd/libcephfs.

- `r := require.New(t)` for concise assertions.
- Prefer table-driven tests for validation and parameterised cases.
- Script-like `t.Run` subtests are good for flow/integration tests
  (see `test/`).
- Test fixtures as `const` when stable.
- `TestMain` cleanup: `defer` does not run with `os.Exit`. Use
  `exitCode := m.Run(); cleanup(); os.Exit(exitCode)`.

## Errors

Use shared sentinels from `pkg/types/errors.go` (`ErrNotFound`,
`ErrAlreadyExists`, `ErrInvalidArg`, `ErrAccessDenied`,
`ErrUnauthenticated`, `ErrInternal`, `ErrNotImplemented`,
`ErrInvalidConfig`). Don't create package-local error variables when a
shared one exists. Compare with `errors.Is`, never `==`. RADOS-specific
sentinels live in the `cgo` / `!cgo` pair
`pkg/types/ceph_errors.go` / `ceph_errors_mock.go`.

Errors from Ceph come back as JSON in stdout plus a status string. See
`rados.Svc.ExecMon` — non-empty `cmdStatus` is logged but doesn't itself
fail the call; check the response JSON for actual errors.

## Parity vs API quality

Dashboard parity tests in `test/parity/` exist to keep ceph-api a
drop-in replacement for the dashboard's REST surface. They do **not**
dictate the gRPC type system. When the two collide, pick the well-typed
proto and absorb the wire-shape divergence in the matcher
(`test/parity/diff.go`) — not by regressing the proto and not by adding
repetitive per-endpoint ignores in `api_diff.yaml`.

- Use `google.protobuf.Timestamp` for timestamps, not unix-seconds ints.
  The matcher coerces RFC3339 ↔ unix-seconds within
  `timestampSkewTolerance`.
- Use `int64` when the upstream C++ type is 64-bit (verify in
  `third_party/ceph/`). protojson serializes int64 as a JSON string and
  the matcher coerces int64-as-string ↔ JSON number.
- The matcher treats `null` on one side and absent on the other as
  equivalent (protojson's `EmitUnpopulated` emits unset proto3-optional
  fields as `null` while the dashboard's hand-rolled JSON omits them).

New shape-class divergences belong in `coerceEqual` in
`test/parity/diff.go` with a unit test, not in `api_diff.yaml`. Reserve
`api_diff.yaml` for genuinely endpoint-specific divergences (a
particular field that's deliberately different from the dashboard).

## Comments

Default to **zero comments**. Only keep one when removing it would
leave a future reader unable to derive a non-obvious invariant,
workaround, or external constraint. Before finishing any edit, re-scan
every comment added; if you can't justify it under one of the three
tests below, delete it.

**Banned:**
- History narration (`legacy`, `previously`, `used to`, `now uses`,
  `added for X`).
- Internal task/session references (`tracked as #123`, `see TASKS.md
  2.1`, `Phase 2.3 summary`).
- Foreign-project name-drops (`from cesto`, `matches chorus pattern`).
- Restating well-named code or paraphrasing identifiers.
- Doc comments that just restate the function signature.

**Keep if any of:**
1. The WHY is non-obvious from reading the surrounding code.
2. A future agent or human couldn't re-derive it without the comment.
3. It describes an external constraint — library contract, protocol
   quirk, framework artifact, regulatory requirement.

Examples that pass: `// fosite sets session.Subject via SetSubject
after Authenticate`, `// grpc-gateway emits {} for Empty proto
responses`, `//nolint:gosec // self-signed cert on localhost-only
listener`.
