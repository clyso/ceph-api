# TASKS

Roadmap for ceph-api automation rollout. Phases are sequential — finish a
phase before starting the next. Within a phase, the listed order is the
suggested execution sequence.

## Orientation (read before starting)

- **What this project is**, the architecture, build/run/test conventions,
  and the `cgo` / `!cgo` build-tag pair: see [CLAUDE.md](./CLAUDE.md).
- **Goal arc**: green make/CI → mechanical harness covers every
  already-implemented endpoint (parity + permission tests) → agents +
  skills → roadmap of remaining endpoints → shakedown one easy endpoint
  end-to-end → establish service-layer pattern on one hard endpoint
  interactively → autonomous pipeline drains the rest → repeat until
  ceph-api replaces every ceph CLI/client surface.
- **Phase-2 mechanical harness** is the thing that makes the rest tractable:
  `test/parity/parity.yaml` is the single source of truth for divergence
  between our API and the upstream dashboard. Every migrated endpoint MUST
  have an entry; an unregistered endpoint fails CI.
- **Verify the current state before starting**:
  ```sh
  make check       # should be green
  make lint        # currently red (Phase 1.1 cleanup)
  make proto       # should be content-idempotent
  make e2e-test    # validates Ceph testcontainers boot + existing e2e tests
  git -C third_party/ceph describe   # v19.2.3
  ```

## Open decisions (resolve before the phase that needs them)

- **Phase 3.4** — should the impl-agent checklist require updating
  `pkg/rados/mock-data/`? Earlier in the design we said "no, mock-data
  routinely lags real Ceph and parity tests catch wire-format bugs".
  Later "checklist with mock update" was raised. Pick one before
  finalising the agent.

---

## Phase 1 — make/CI green

Phase exit criterion: `make full-gate` exits 0 locally and CI's `test` job
passes without `continue-on-error` anywhere.

- [x] **1.1 Fix pre-existing golangci-lint findings** —
  `make lint` surfaces ~15 issues today (1 ineffassign, 3 nolintlint,
  2 prealloc, 9 staticcheck). Categories:
  - Deprecated grpc dial APIs (`grpc.WithInsecure`, `grpc.DialContext`,
    `grpc.WithBackoffMaxDelay`, `grpc.WithBlock`) in
    `test/setup_cgo_test.go`, `go_api_client.go`. Replace with
    `grpc.NewClient` + `grpc.WithTransportCredentials(insecure.NewCredentials())`
    + `grpc.WithConnectParams(...)`.
  - `github.com/golang/protobuf/proto` in `pkg/api/grpc_server.go` →
    `google.golang.org/protobuf/proto`.
  - Redundant `int` type decls (`var expIn int = int(...)` →
    `expIn := int(...)`) in `pkg/api/users_api_handlers.go`.
  - Capitalised error strings in `pkg/types/ceph_errors_mock.go`.
  - Slice preallocation hints in `pkg/api/status_api_handlers.go`.
- [x] **1.2 Clean up `lima-ceph-dev.yaml`** —
  `buf`, `protoc-gen-go`, `protoc-gen-go-grpc`, `protoc-gen-grpc-gateway`,
  `protoc-gen-openapiv2` are now `tool` directives in `go.mod` invoked via
  `go tool <name>`. Drop the corresponding `sudo go install` lines from
  the Lima provisioning script.
- [x] **1.3 Upgrade go.mod deps and Go version** —
  `go get -u ./... && go mod tidy`, bump the `go` directive in `go.mod` to
  the latest stable, bump `golang:X` in the production `Dockerfile`. CI's
  `go-version-file: go.mod` will follow automatically. Validate
  `make check` + `make e2e-test` after.

---

## Phase 2 — mechanical harness, applied to all existing endpoints

Phase exit criterion: every already-implemented endpoint (`cluster`,
`users`, `crush_rule`, `status`, `auth`) is covered by parity + permission
tests; `make full-gate` is green in CI.

- [x] **2.1 Build the dashboard-parity contract framework** —

  **Goal.** Drop-in compatibility: every endpoint we expose must
  produce response bodies byte-equivalent to the dashboard's, modulo
  declared ignores in `test/parity/api_diff.yaml`. Adding a new gRPC
  method, HTTP route, or parity test omission is caught mechanically
  by one of three layered checks (below).

  **Sources of truth.**
  - `api/http.yaml` — what ceph-api exposes (gateway selector →
    method + path). This is the inventory the parity framework reads
    to enforce coverage; the swagger JSON and any generated inventory
    file are not used.
  - `third_party/ceph/src/pybind/mgr/dashboard/openapi.yaml` — what
    the dashboard exposes. Used to detect ceph-api routes that have
    no dashboard counterpart, so the parity recorder can skip the
    dashboard side for them. Path placeholders are normalized to
    `{}` on both sides so `/api/role/{name}` (ours) matches
    `/api/role/{role_name}` (dashboard).

  **Three layered checks, all firing in `TestMain` after `m.Run()`:**

  1. **gRPC method → http.yaml selector.** Walk
     `protoreflect.GlobalFiles` for every registered gRPC service +
     method (the generated `*.pb.go` packages register descriptors on
     import). Diff against http.yaml selectors. Missing → fail with
     "add a rule for `ceph.Service.Method` in `api/http.yaml`."
     Forces every new RPC to be HTTP-exposed.

  2. **http.yaml route → parity test.** Set of `(METHOD, PATH-TEMPLATE)`
     from http.yaml, excluding the `/api/auth/*` prefix (the parity
     clients use auth for their own bootstrap). Set of routes
     exercised by a parity test, accumulated at runtime in
     `parity.coverage` (mutex-guarded). Diff → fail naming each
     missing route and the test-file-per-service convention.

  3. **`Call` → http.yaml route** (per recorded call). When a parity
     test passes a `Call` to the recorder, the recorder looks up
     `(Method, Path)` in the http.yaml route set. Missing → immediate
     `t.Fatalf` with closest matches, so a typo like `/api/role/{Name}`
     fails at the call site instead of silently leaving the real route
     uncovered.

  **Recorder API.**

  - `parity.Init(dash, ours *Client, httpYAMLPath, dashboardSwaggerPath, apiDiffPath string)`
    — called once from `runSetup` after auth bootstrap. Loads
    http.yaml + dashboard openapi.yaml + api_diff.yaml into
    package-level state, sets the two backend clients.
  - `parity.Backend` enum: `Dash`, `Ours`. `parity.Backends` is the
    `[]Backend{Dash, Ours}` iteration slice.
  - `parity.New(t testing.TB) *Recorder` — per-test Recorder pulling
    from package state; registers `t.Cleanup(r.assertAll)`.
  - `r.Backends(c Call) []Backend` — returns `[Dash, Ours]` if the
    dashboard openapi.yaml declares this route's shape, else `[Ours]`.
    Tests iterate this instead of `parity.Backends` so ceph-api-only
    routes are exercised on ours but not parity-compared.
  - `r.Do(b Backend, c Call)` — sends without recording (use for
    prep/cleanup).
  - `r.DoRecord(b Backend, c Call)` — sends + records for cleanup-time
    comparison and coverage.
  - `parity.Call`: `{Method, Path (template), PathParams, QueryParams,
    Body, Accept, Headers http.Header}`. No `NoRecord` flag —
    `Do` vs `DoRecord` carries that distinction at the call site.
  - Both methods return `(*http.Response, []byte)` so tests can
    `require.Equal(t, 200, resp.StatusCode)` on prep/cleanup.

  **Defensive checks at record time (fail at the offending call):**
  - Empty `Method` or `Path` → `t.Fatalf`.
  - `(Method, Path)` not in http.yaml → `t.Fatalf` with closest matches.
  - Same `(endpoint id, backend)` recorded twice in one test →
    `t.Fatalf` naming both file:line locations. Pairing is strict 1:1
    per test; split into multiple tests for repeated flows.
  - Second backend's request disagrees with first (method,
    substituted path, query, body, accept) → `t.Fatalf` showing the
    diff. Parity demands byte-identical requests on both sides.

  **Cleanup (`r.assertAll` in `t.Cleanup`):**
  - Ours-only AND dashboard openapi.yaml has no shape match → mark
    covered, no diff. ceph-api-only route by design.
  - Ours-only AND dashboard has the shape → `t.Errorf` (test used a
    raw loop instead of `r.Backends(call)`, or recorded only one
    side).
  - Dash-only → `t.Errorf` (test forgot ours).
  - Both present → mark covered, compare bodies via
    `parity.Compare(dashJSON, oursJSON, ignores)` with ignores from
    `api_diff.yaml[endpoint id]`. Divergences → `t.Errorf` naming
    endpoint id, file:line, and each diff path.

  **`test/parity/api_diff.yaml`** — engineer-curated divergence
  catalogue. Keyed by endpoint id (`"<METHOD> <PATH-TEMPLATE>"`).
  Values are lists of JSONPath ignores with mandatory `reason:`. This
  file IS the migration guide for dashboard clients. Empty initially;
  Phase 2.3 populates it.

  ```yaml
  "GET /api/cluster":
    - path: $.uptime
      reason: live; varies between calls
  ```

  **Parity tests.** One file per service in `test/`:
  `cluster_parity_test.go`, `crush_rule_parity_test.go`,
  `status_parity_test.go`, `users_parity_test.go`. Each test:

  ```go
  func Test_Parity_Role_CRUD(t *testing.T) {
      r := parity.New(t)
      create := parity.Call{Method: "POST", Path: "/api/role", Body: ..., Accept: roleAccept}
      get    := parity.Call{Method: "GET",  Path: "/api/role/{name}", PathParams: ..., Accept: roleAccept}
      del    := parity.Call{Method: "DELETE", Path: "/api/role/{name}", PathParams: ..., Accept: roleAccept}

      for _, b := range r.Backends(create) {
          resp, _ := r.DoRecord(b, create); require.Equal(t, 201, resp.StatusCode)
          r.DoRecord(b, get)
          resp, _ = r.DoRecord(b, del); require.Equal(t, 204, resp.StatusCode)
      }
  }
  ```

  Outer loop = `r.Backends(canonicalCall)` (dash runs full flow,
  including its own DELETE, before ours starts → state is clean for
  ours pass; for ceph-api-only flows the loop only runs ours). Inner
  loop = the call sequence. Use `r.Do(b, ...)` for defensive
  pre-cleanup of leftover state from a previous failed run.

  **State model.** One shared Ceph cluster (`testenv.CephEnv`, started
  in `TestMain`). Fixture names are unique per test. POST timestamps
  and other live fields land in `api_diff.yaml` per endpoint.

  **Auth.** `/api/auth/*` is excluded from the http.yaml → parity-test
  coverage gate; the bootstrap login in `test/setup_cgo_test.go`
  already exercises it on both backends. Dedicated auth parity tests
  can be added later if we want to verify token-issuance shape.

- [ ] **2.2 Build the permission-test framework** —
  Table-driven helper. For each gRPC method × each permission scope in
  `pkg/user/system_roles.go`, verify a user lacking the scope gets
  `PermissionDenied` and a user holding it succeeds. One `<service>_permissions_test.go`
  per service in `test/`.

- [x] **2.3 Populate `api_diff.yaml` for all 5 existing services** —
  Run the parity tests from 2.1 and watch them fail. For every endpoint
  that surfaces divergence, decide: is this a wire-format bug in
  ceph-api (fix it) or an intentional divergence (declare ignores in
  `test/parity/api_diff.yaml` with mandatory `reason:`). This is the
  moment to lock down what we promise to match vs intentionally
  diverge.

  Dashboard versioned `Accept` headers: the dashboard returns 415
  "Unable to find version in request header" without
  `application/vnd.ceph.api.vX.Y+json`. Look up the per-endpoint
  version in `third_party/ceph/src/pybind/mgr/dashboard/openapi.yaml`
  and pass it on the parity `Call.Accept` field; the recorder forwards
  the same value to both backends.

  **Rule for future agents: parity must not regress the proto API.**
  When the dashboard's JSON shape collides with idiomatic proto types
  (e.g. dashboard emits unix-seconds integer while a `Timestamp` field
  serializes as RFC3339), the proto stays well-typed and the matcher
  in `test/parity/diff.go` absorbs the encoding diff via `coerceEqual`.
  See CLAUDE.md → "Parity vs API quality" for the decision rule and
  the currently-handled cases (Timestamp ↔ unix-seconds, int64-string
  ↔ JSON number, null ↔ absent). New convention classes belong in
  `coerceEqual` with a unit test, not in `api_diff.yaml`. Reserve
  `api_diff.yaml` for genuinely endpoint-specific divergences.

- [ ] **2.4 Populate permission tests for all 5 existing services** —
  Apply the framework from 2.2 to every service. Each service's table
  covers every method × every relevant scope.

- [ ] **2.5 Bring `make full-gate` to green in CI** —
  After 2.1–2.4 land, `make full-gate` runs `check` + `lint` + `proto`
  (idempotent) + `e2e-test` (now includes parity + permission tests).
  Verify green on first push.

---

## Phase 3 — agents and skills

Phase exit criterion: investigation + impl + review agents exist and can,
under interactive supervision, walk one chosen endpoint through the
pipeline to merge.

- [ ] **3.1 Mine cesto + chorus `.claude/` configs** —
  Inspect `~/projects/clyso/cesto/.claude/{agents,skills,settings.local.json}`
  and `~/projects/github.com/clyso/chorus/.claude/` (if present). Extract
  patterns that map to ceph-api's scope (no DST, no ADR, simpler review).
  Adapt — don't copy wholesale.

- [ ] **3.2 Create the project skill at `.claude/skills/ceph-api/`** —
  Index:
  - `api/openapi/ceph-api.swagger.json` (our published API).
  - `third_party/ceph/src/pybind/mgr/dashboard/` (dashboard source — what
    we shadow).
  - `third_party/ceph/src/pybind/mgr/restful/` (restful module source).
  - `third_party/ceph/src/mon/MonCommands.h`, `src/mgr/*.cc` (CLI command
    defs).
  - `~/projects/ora/liquid-ceph/orpheus/` (non-JSON RADOS command parsing
    reference for go-ceph use cases).
  - `pkg/user/system_roles.go` (permission scopes — don't invent new ones).
  - One worked-example endpoint walkthrough + the parity.yaml entry format.

- [ ] **3.3 Build the investigation agent** —
  This is where the judgment lives. Equipped with the project skill (3.2),
  a running `testenv.CephEnv` for `ceph` CLI / log access, and cross-
  release diff tooling (`make ceph-ref-versions` then
  `git -C third_party/ceph diff v18.2.7..v19.2.3 -- <path>`). Output
  contract: an RPC-mapping doc per endpoint — which mon/mgr command, JSON
  payload shape, response shape, version-stability notes, what entries
  to put in `parity.yaml` ignores.

- [ ] **3.4 Build the impl agent as a checklist runner** —
  Endpoint work is typical; the agent runs a fixed checklist:
  1. Edit/create `api/<service>.proto`, route in `api/http.yaml`.
  2. `make proto`.
  3. Handler in `pkg/api/<service>_api_handlers.go`.
  4. Wire into `pkg/app/start.go` + `pkg/api/grpc_server.go` +
     `pkg/api/grpc_http_gateway.go` if a new service.
  5. (Optional) Service layer in `pkg/<domain>/` when logic exceeds JSON
     pass-through. Pattern established in Phase 6.
  6. E2E test in `test/`.
  7. Permission test row added to the service's permissions test.
  8. Parity entry in `test/parity/parity.yaml`.
  9. `make check`, `make e2e-test`.

  **Blocked on the open decision above** — does the checklist include a
  step to update `pkg/rados/mock-data/`?

- [ ] **3.5 Build the review agent** —
  Light review. Verify: required tests exist (e2e + permission + parity),
  all make gates pass, file-splitting and naming match exemplars, no slop
  comments, no abstractions beyond what the task needs.

- [ ] **3.6 Trim `CLAUDE.md`** —
  CLAUDE.md is currently bloated because the harness conventions, build
  flow, and architecture all live there as a fallback for tools that don't
  load skills. Once the project skill at `.claude/skills/ceph-api/` (3.2)
  exists and is the authoritative source for paths, conventions, and
  worked examples, strip CLAUDE.md back to: a one-paragraph project
  summary, a pointer to the skill, and the unavoidable Claude-Code-only
  conventions. Aim for ~30 lines.

---

## Phase 4 — roadmap

Phase exit criterion: `tasks/endpoints/` holds one markdown file per
unmigrated endpoint, each self-sufficient for an autonomous agent to pick
up.

- [ ] **4.1 Generate one task file per unmigrated endpoint** —
  Diff our `api/openapi/ceph-api.swagger.json` against the upstream
  dashboard swagger (find it under `third_party/ceph/` — likely in
  `src/pybind/mgr/dashboard/` or generated at runtime; investigate). Emit
  one markdown file per missing endpoint at
  `tasks/endpoints/<service>-<operation>.md` with:
  - Dashboard route + HTTP method.
  - Suggested proto name + service.
  - Scope: `mon` (simple) / `mgr` (orchestration) / `python-reimpl` (the
    dashboard does real logic we'd need to port).
  - Known dependencies (other endpoints, prior tasks).

  **Do not group endpoints**, even small related ones. One file per
  endpoint so autonomous agents can claim them independently.

---

## Phase 5 — shakedown one easy endpoint

Phase exit criterion: one mon-command-based endpoint has been driven
end-to-end through the agent pipeline (investigation → impl → review),
under interactive supervision, and merged. Friction observed during the
run has been fed back into agent/skill tweaks.

- [ ] **5.1 Pick a simple endpoint and run it end-to-end** —
  Choose something mon-command-based, low risk. Watch the agents work it.
  Note where the checklist surfaces gaps, where tests miss things, where
  review catches or misses. Adjust 3.2–3.5 accordingly.

---

## Phase 6 — establish service-layer pattern on one hard endpoint

Phase exit criterion: a `pkg/<domain>/` service-layer package exists for
one orchestration-heavy endpoint, with handler in `pkg/api/` reduced to a
thin proto↔service-type adapter. This file layout becomes the canonical
pattern that the impl agent's checklist step 5 references for all
subsequent non-trivial endpoints.

- [ ] **6.1 Pick one mgr-module / orchestration-heavy endpoint and migrate it interactively** —
  Currently migrated endpoints are JSON pass-throughs to mon. Many
  remaining ones need real logic (re-implement dashboard orchestration,
  parse non-JSON RADOS output, manage state, etc.). Do this one
  **interactively, by hand**: handler stays thin in `pkg/api/`,
  orchestration lives in a new `pkg/<domain>/` package, testable
  independently of proto. Once stable, update the project skill (3.2)
  with the new pattern and a worked-example walkthrough.

---

## Phase 7 — autonomous pipeline

Phase exit criterion: most of `tasks/endpoints/*.md` is drained without
human-interactive sessions; humans only review merge candidates.

- [ ] **7.1 Let the agents drain the roadmap** —
  With the pattern (6.1) established and pipeline (5.1) proven,
  autonomous agents pick up remaining `tasks/endpoints/*.md` items.
  Iterate on agents/skills as new edge cases surface.

---

## Phase ∞ — beyond dashboard parity

Once dashboard parity is complete, expand scope to cover the rest of the
`ceph` CLI surface that has no dashboard equivalent (admin sockets,
RGW admin API, `radosgw-admin`, RBD/CephFS-specific subcommands). Same
pipeline applies; the parity contract may evolve into a more general
"ceph CLI parity" framework.

- [ ] **∞.1 Catalogue the post-dashboard surface** — survey what ceph CLI
  commands are still uncovered after Phase 7.
- [ ] **∞.2 Decide on parity reference per command class** — admin socket
  has no HTTP equivalent, so the parity contract has to evolve.
- [ ] **∞.3 Run the pipeline against the new catalogue**.

---

## Deferred / low-priority cleanup

- [ ] **D1 Replace `writeFile` heredoc with `CopyToContainer` in
  `test/testenv/ceph.go`** — Current `writeFile` uses
  `sh -c "cat > <path> <<'EOF'\n<content>\nEOF\n"`. Single-quoted `'EOF'`
  blocks expansion so the only caller (dashboard password) is safe, but
  content containing the literal line `EOF` would terminate the heredoc
  early. Replace with `testcontainers.Container.CopyToContainer`.

- [ ] **D3 Fix HTTP gateway TLS verification path in Secure mode** —
  `pkg/api/grpc_http_gateway.go` dials the in-process gRPC server over TLS
  with a default `tls.Config{}` (no custom CA pool, no `ServerName`
  override, no `InsecureSkipVerify`). The server presents a self-signed
  cert from `selfIssuedTlsConf()` (CN `seaphony.github.com`), so
  verification cannot succeed against an arbitrary listen address. This
  path was already broken before the lint cleanup (a misleading
  `InsecureSkipVerify: false` `nolint:gosec` directive masked it). Either
  `conf.Api.Secure` is unused in practice, or the gateway can't talk to
  its own backend in TLS mode. Decide: drop Secure-mode altogether, or
  fix this dial path (skip-verify for loopback, or load the self-issued
  cert into a `RootCAs` pool, or override `ServerName`).

- [ ] **D2 Refactor `-tid` to avoid host networking** —
  Pre-create the docker network in `test/testenv/tid.go` before spawning
  the inner test container, pass its name via env, have `CephEnv` attach
  Ceph to that pre-existing network. Connect via container alias `ceph`
  instead of static IP 192.168.56.7. Drops `NetworkMode = "host"`,
  `EndpointSettingsModifier`, the static-IP plumbing. **Low priority** —
  current host-networking approach works on Linux native, Colima
  (user uses this on macOS), Lima, and Docker Desktop with the
  host-networking toggle. Refactor only if a portability case bites.
