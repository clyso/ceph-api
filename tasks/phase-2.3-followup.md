# Phase 2.3 follow-up — retire root `$` ignores in `test/parity/api_diff.yaml`

Read project `CLAUDE.md` first. Don't `git commit` — user reviews and commits manually.

## Goal

Every `path: $` entry in `test/parity/api_diff.yaml` is body-shape debt
between ceph-api and the upstream Ceph dashboard. Retire each one by
fixing ceph-api to match the dashboard's wire shape. If that's not
feasible, replace with tightly-scoped per-field ignores.

## Rules

- An endpoint can be excluded from the dashboard pass only via
  `/api/auth*` prefix or absent-from-dashboard-openapi. No manual
  skip list — reintroducing one is a regression.
- Every recorded call must return 2xx on both backends; assert at
  the call site with `require.True`.
- After every change: `make lint && make check`. Then `make e2e-test`
  (docker / colima up on macOS).
- `make proto` after any `.proto` edit.

## Open `path: $` ignores

| Endpoint | Divergence | Fix |
|---|---|---|
| `POST /api/cluster/user`, `PUT /api/cluster/user` | dash returns status-string; ours returns Empty | change RPC return type; have the handler return the resource (or a `google.protobuf.StringValue` if matching dashboard's string body) |
| `POST /api/cluster/user/export` | cephx key is random per `auth add` so the two backends' exports differ only in the key | seed both creates with `import_data` carrying a fixed key, re-export |
| `POST /api/role`, `PUT /api/role/{name}` | dash returns the resource; ours Empty | change RPC return type to `Role`; read role back after the write |
| `POST /api/user`, `PUT /api/user/{username}` | dash returns the resource; ours Empty | change RPC return type to `User`; read user back after the write |
| `POST /api/user/{username}/change_password` | error-path fixture (admin lacks self-change context); error envelopes diverge | replace with positive fixture — create a test user, log in as it, change pw, expect 2xx |
| `POST /api/crush_rule` | dash returns the Rule; ours Empty | change RPC return type to `Rule`; read the rule back from `osd crush dump` |
| `GET /api/user` | list-length differs (dashboard caches user table at startup; ceph-api writes via `mgr/dashboard/accessdb_v2` aren't seen) | reload the dashboard's accessdb in `testenv` before the parity pass, or drop the list from parity in favor of a coverage-only ours-side test |

## Per-field ignores worth closing

- `GET /api/user/{username}` — dashboard's User module uses camelCase
  (`lastUpdate`, `pwdUpdateRequired`, `pwdExpirationDate`); ours snake_case
  via project-wide `UseProtoNames: true`. Add per-field `json_name`
  annotations on the User proto so just these three fields emit
  camelCase. The `name`/`email` ignores are both-null false positives
  — remove them.
- `GET /api/crush_rule` and `GET /api/crush_rule/{name}` — int64
  protojson-as-string (consider int32 in proto if precision allows);
  Step shape rewrite from `map<string,string> entries` to typed
  `{op, item, item_name, num, type}`. Heavier — split off if needed.

## Optional plumbing

`parity.Init` currently takes three file paths. Cleaner: take `[]byte`
per source so callers use `//go:embed` and the parity package never
touches the filesystem. Files stay where they are.

## Done when

- `test/parity/api_diff.yaml` has zero `path: $` entries (or each
  remaining one has a non-trivial reason).
- `make full-gate` exits 0.
- Every parity test asserts 2xx at every call site.
