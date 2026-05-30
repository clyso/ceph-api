# Parity tests

These tests keep ceph-api a **drop-in replacement** for the Ceph
dashboard's REST surface: they fire the same HTTP request at both the
real dashboard and ceph-api and assert the responses match. Read the
root `CLAUDE.md` "Parity vs API quality" section first — it governs how
divergences are resolved.

## The contract (mechanical gate)

The dashboard `openapi.yaml` is the source of truth. Two gates run in
`runSetup` (called by `TestMain`) after the suite (`test/setup_cgo_test.go`):

- **`AssertGRPCMethodsRouted`** (`grpc.go`) — every gRPC method must have
  an `api/http.yaml` route. Only `grpc.*` / `opentelemetry.*` infra
  prefixes are exempt.
- **`AssertRoutesCovered`** (`inventory.go`) — every `http.yaml` route
  must be exercised by a parity test. The **only** excluded prefix is
  `/api/auth` (the bootstrap login already covers it and parity clients
  can't dogfood their own auth flow).

For a route, `r.Backends(call)` returns `[Dash, Ours]` if the route also
exists in the dashboard openapi, else `[Ours]` only. So a route is
diffed against the dashboard iff it is in **both** `http.yaml` and the
dashboard openapi. Endpoints absent from the dashboard openapi still need
a parity test (to satisfy coverage) but are only recorded on our side, no
diff. **There is no manual skip list — adding one is a regression.**

## Files

| File | Role | |
|---|---|---|
| `recorder.go` | Per-test harness: `Call`, `New(t)`, `DoRecord`/`Do`/`DoRecordAs`, request canonicalization, coverage map, `t.Cleanup` diff (`assertAll`). | **FRAMEWORK — do not edit** |
| `probes.go` | Authz probes: after each recorded `Ours` call, asserts 401 (no token) and 403 (no perms). | **FRAMEWORK — do not edit** |
| `inventory.go` | Loads `http.yaml` + dashboard openapi; shape matching; coverage gate. | **FRAMEWORK — do not edit** |
| `diff.go` | JSON comparator `Compare` + `coerceEqual` tolerances + JSONPath ignore matcher. | **FRAMEWORK — do not edit** |
| `api_diff.go` | Parses/validates `api_diff.yaml` (`path` + `reason` mandatory). | **FRAMEWORK — do not edit** |
| `client.go`, `grpc.go` | Auth'd HTTP client; gRPC-routed gate. | **FRAMEWORK — do not edit** |
| `api_diff.yaml` | Declared per-endpoint divergence ignores (data). | **editable (data only)** |
| `test/*_parity_test.go` | The scenarios. | **editable — add tests here** |

A port's **only** allowed edits in this area are: a new
`test/<svc>_parity_test.go` scenario, and (rarely, justified)
`api_diff.yaml`. Never modify framework code to make a port pass — that
is how you defeat the gate, and the reviewers check for it.

## What the matcher coerces for free (`coerceEqual` in `diff.go`)

You do **not** need an `api_diff.yaml` entry for any of these:

- **RFC3339 timestamp string ↔ unix-seconds number** (5s skew tolerance,
  absorbs the wall-clock gap between the two sequential requests).
- **int64-as-JSON-string ↔ JSON number** (protojson emits int64 as a
  string).
- **null ↔ absent** (protojson `EmitUnpopulated` emits unset optionals as
  `null`; the dashboard omits them).
- **empty body ↔ `{}` ↔ `null`** (a dashboard 204 pairs with our
  `Empty`-proto `{}`).

Only status **class** (`/100`) is compared, not exact codes. What you
must still make match: differing scalar values, non-coercible type
mismatches, array length, missing/extra non-null keys, and field-name
casing (snake vs camel — fix with proto `json_name`, not a yaml ignore).

## Adding a scenario

1. Add `Test_Parity_<Service>_<Case>` to `test/<service>_parity_test.go`
   (`//go:build cgo`, `package test`).
2. `r := parity.New(t)`; build `parity.Call{Method, Path, Accept, Body}`.
   `Method`+`Path` must exactly match an `http.yaml` rule. Set `Accept`
   to the dashboard's per-route versioned media type (else dashboard
   returns 415).
3. Drive both backends and assert 2xx at every recorded call:

   ```go
   func Test_Parity_CrushRule_List(t *testing.T) {
       r := parity.New(t)
       call := parity.Call{Method: "GET", Path: "/api/crush_rule", Accept: crushRuleReadAccept}
       for _, b := range r.Backends(call) {
           resp, _ := r.DoRecord(b, call)
           require.True(t, resp.StatusCode/100 == 2, "%s: status %d", b, resp.StatusCode)
       }
   }
   ```

4. **Setup/cleanup so scenarios don't interfere:** use unique fixture
   names; do prep/cleanup with `r.Do` (not `DoRecord` — those aren't
   recorded/diffed); register `t.Cleanup(func(){ r.Do(parity.Ours, del) })`.
   For CRUD, `Backends` runs `Dash` first so its create+delete clean up
   before the `Ours` pass.
5. A specific-user endpoint (e.g. self `change_password`): `parity.Login`
   then `r.DoRecordAs(b, call, userClient)`.
6. Invariants: recording the same endpoint twice in one test fatals
   (split tests). The Dash and Ours requests for an endpoint must be
   byte-identical except Authorization/Content-Length.

## api_diff.yaml policy

Entry schema — keyed by `"<METHOD> <PATH-TEMPLATE>"`, value a list of
`{path, reason}` (both mandatory). `path` is a JSONPath subset: `$` root,
`.` descent, `*` single wildcard segment; each pattern must equal the
path length (no recursive descent).

```yaml
"GET /api/crush_rule/{name}":
  - path: $.some_field
    reason: dashboard returns X here; ours returns Y because <reason>
```

`path: $` ignores the **whole body** (and suppresses presence/status
checks) — pure debt, tracked for retirement. Policy: minimize entries;
only for genuinely endpoint-specific divergences; never for a shape-class
`coerceEqual` already handles; **prefer fixing the proto (Timestamp,
int64, `json_name`) or extending `coerceEqual` (with a `diff_test.go`
test) over a yaml ignore.** The proto type system wins over parity;
absorb wire-shape divergence in the matcher, not by regressing the proto.
