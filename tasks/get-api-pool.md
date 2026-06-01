# Port ceph dashboard endpoint GET /api/pool

## Requirements

### 1. Summary

Lists all pools with their osd_map attributes. Maps to the dashboard
`Pool.list` controller method
(`third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:148-150`,
the `RESTController.list` of `class Pool`). Extends the existing `Pool`
gRPC service (`api/pool.proto`) with a `ListPools` rpc — do **not** create
a new service.

### 2. Data source

`reconstruct`.

The endpoint fires **no mon command** — verified live: curling `/api/pool`
(both default and `?stats=true`) produced **zero** new `dispatch` lines in
`ceph.audit.log`. The controller reads mgr's cached cluster map
(`mgr.get('osd_map')['pools']`, via `CephService.get_pool_list`,
`third_party/ceph/src/pybind/mgr/dashboard/services/ceph_service.py:119-125`)
plus the crush rule names (`mgr.get('osd_map_crush')['rules']`,
`pool.py:107`).

**Reconstruction strategy (default, `stats` omitted/false):**

1. `osd dump` (mon, `format=json`) → `.pools[]` is the same per-pool object
   the dashboard returns. Verified field-for-field against the live REST
   body (raw `osd dump .pools[0]` == REST `[0]` modulo the 4 transforms
   below). The dashboard's `mgr.get('osd_map')['pools']` and the mon `osd
   dump` `pools` array share one schema.
2. `osd crush dump` (mon, `format=json`) → `.rules[]` gives the
   `{rule_id → rule_name}` map for the `crush_rule` transform.

Apply these handler-side transforms in `_serialize_pool`
(`pool.py:99-129`) to each pool:
- `type`: int → string — `1 → "replicated"`, `3 → "erasure"`.
- `crush_rule`: int rule_id → rule_name string (lookup in the crush map).
- `application_metadata`: object `{"rgw":{}}` → list of its keys `["rgw"]`.
- `read_balance`: any `float` that is `inf` → the string `"Infinity"`
  (`pool.py:117-123`). See the inf/nan quirk note in §6.

`pool_name` is always present in the output even under `attrs` filtering
(`pool.py:128`).

The `stats=true` path is a **documented divergence**, not part of the core
reconstruct — see §11. It depends on mgr-internal time-series
(`mgr.get("pg_summary")`, `mgr.get_updated_pool_stats()`,
`get_time_series_rates`, `ceph_service.py:127-150`) which is the stateful
mgr feature CLAUDE.md tells us not to reimplement now.

### 3. Scope & permission

First statement of the handler:

```go
if err := user.HasPermissions(ctx, user.ScopePool, user.PermRead); err != nil {
    return nil, err
}
```

Derived from `@APIRouter('/pool', Scope.POOL)` on `class Pool`
(`pool.py:92`); `Scope.POOL == "pool"` → `user.ScopePool`. `list` has no
explicit `@*Permission` decorator, so the `RESTController` GET→read
fallback applies (`permissions.md` "Default permission") → `user.PermRead`.
No multi-scope, no dashboard-settings gotcha.

### 4. Accept version

`application/vnd.ceph.api.v1.0+json` (from `openapi.yaml`, `/api/pool` GET).
Wrong version → 415.

### 5. Request

No body. Two query params (`pool.py:148`, `openapi.yaml`):

| field | in | proto type | notes |
|---|---|---|---|
| `attrs` | query | `string` | Comma-separated whitelist of attribute names to return. Default absent → all attributes. **Response-shaping param — cannot be honored by a typed/Struct proto under `EmitUnpopulated`; see §11.** |
| `stats` | query | `bool` | Default `false`. When true, augments each pool with `pg_status` + `stats`. **Out of scope; see §11.** |

No `**kwargs`, no flat-open-key hazard on this endpoint.

### 6. Response

`200`: a **bare JSON array** of pool objects (unwrap with
`response_body` so the body is the array, not `{pools:[...]}`). Each pool
is a large, heterogeneous object. Real sample (one element, default
params) — abbreviated, all 62 top-level keys present in the live body:

```json
{
  "pool": 1,
  "pool_name": ".rgw.root",
  "flags": 1,
  "flags_names": "hashpspool",
  "type": "replicated",
  "size": 1, "min_size": 1,
  "crush_rule": "replicated_rule",
  "object_hash": 2,
  "pg_autoscale_mode": "off",
  "pg_num": 8, "pg_placement_num": 8, "pg_num_target": 8,
  "last_pg_merge_meta": { "ready_epoch": 0, "source_pgid": "0.0",
                          "source_version": "0'0", "target_version": "0'0", ... },
  "quota_max_bytes": 0, "quota_max_objects": 0,
  "tiers": [], "tier_of": -1, "read_tier": -1, "write_tier": -1,
  "cache_mode": "none",
  "erasure_code_profile": "",
  "hit_set_params": { "type": "none" },
  "use_gmt_hitset": true,
  "grade_table": [],
  "options": { "pg_num_max": 32, "pg_num_min": 1 },
  "application_metadata": ["rgw"],
  "read_balance": { "score_type": "Fair distribution", "score_acting": 1.0,
                    "primary_affinity_weighted": 1.0, ... },
  "create_time": "2026-06-01T11:00:00.246525+0000",
  "last_change": "9",
  "last_force_op_resend": "0",
  "last_force_op_resend_prenautilus": "0",
  "last_force_op_resend_preluminous": "0",
  "removed_snaps": "[]"
}
```

**Proto-type / shaping notes (for the implementer + matcher):**

- Recommended modeling: the pool object is highly heterogeneous and
  version-variant (different pools carry different `options` keys, optional
  `read_balance`, `last_pg_merge_meta`), so model each element as
  `google.protobuf.Struct` and the response as `repeated Struct` (or a
  message wrapping `repeated Struct` unwrapped via `response_body`). A
  hand-typed message for 60+ drifting fields is brittle; `Struct` round-
  trips the wire faithfully and the parity matcher compares JSON.
- Handler must perform the four transforms listed in §2 (int→enum-string
  for `type`, id→name for `crush_rule`, object→key-list for
  `application_metadata`, inf→`"Infinity"` for `read_balance`). The parity
  matcher does **not** do these; they are real data reshapes done in
  Python, so the Go handler must replicate them on the raw `osd dump`
  bytes.
- **inf/nan quirk:** `read_balance` scores are floats that Ceph's JSON
  formatter can emit as bare `inf`/`-inf`/`nan` tokens
  (`src/common/Formatter.cc`), which Go's `encoding/json` rejects. The
  dashboard converts only `inf` floats to `"Infinity"`. The handler must
  sanitize the raw `osd dump` bytes before unmarshalling (the ceph-src skill
  quirk) **and** then apply the dashboard's inf→`"Infinity"` substitution
  in `read_balance` to match parity. (Live cluster showed finite `1.0`
  values, but the conversion path is in the controller and must be ported.)
- Stringified fields that look numeric but are JSON strings on the wire —
  keep them as strings to match: `last_change`, `last_force_op_resend*`
  (`"9"`, `"0"`), `last_pg_merge_meta.source_version`/`target_version`
  (`"0'0"`), and `removed_snaps` (the stringified `"[]"`). Using `Struct`
  preserves these verbatim.
- `pool` is a pool id (small int). 64-bit counters like `quota_max_bytes`
  appear as JSON numbers here; `Struct` represents them as `double` —
  acceptable for parity (matcher coerces number shapes), and avoids the
  int64-as-string concern of a typed field.

Statuses: `200` on success (even for an empty cluster → `[]`). Authz: `401`
(no token) / `403` (no `pool:read`) per the parity authz probes.

### 7. Underlying mechanism

**No mon command is required for the dashboard's data** (it reads the mgr
osd_map cache). For the Go reconstruct, the verified equivalent mon
commands are:

1. `{"prefix": "osd dump", "format": "json"}` — returns `.pools[]`.
   Confirmed: the raw `osd dump` pool object equals the dashboard pool
   object before the four transforms. (`MonCommands.h`: `osd dump`.)
2. `{"prefix": "osd crush dump", "format": "json"}` — returns `.rules[]`
   with `rule_id`/`rule_name` for the `crush_rule` name lookup.

Both are pure reads; neither writes to `ceph.audit.log` as a state change.
(Audit confirmation: a fresh GET added zero `dispatch` lines.)

### 8. gRPC service decision

Extend the existing `Pool` service in `api/pool.proto` — add
`rpc ListPools (google.protobuf.Empty) returns (ListPoolsResponse)` (or a
small request message carrying `attrs`/`stats` for swagger completeness,
even though both are out-of-scope/divergent; see §11). Do **not** create a
new service. `google/protobuf/struct.proto` is already imported there.

### 9. Closest in-repo analogue

`pkg/api/crush_rule_api_handlers.go` → `ListRules`
(`crush_rule_api_handlers.go:126-140`): the matching `reconstruct`-class
read — `user.HasPermissions(..., PermRead)` first, single
`ExecMon` of an `osd crush dump` JSON command, `json.Unmarshal` into a
proto response, return. Its e2e + parity tests
(`test/crush_rule_api_test.go`, `test/crush_rule_parity_test.go`) are the
templates for `ListPools`. The new handler differs in: two ExecMon calls
(dump + crush dump), the four reshape transforms, and the inf-sanitize
step.

### 10. Relevant src files

- `third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:92` —
  `@APIRouter('/pool', Scope.POOL)` (scope source).
- `.../controllers/pool.py:99-129` — `_serialize_pool`: the four
  transforms (type, crush_rule, application_metadata, read_balance inf).
- `.../controllers/pool.py:131-150` — `_pool_list` + `list` (attrs split,
  stats branch, the `list` method itself).
- `.../services/ceph_service.py:119-125` — `get_pool_list` (reads
  `mgr.get('osd_map')['pools']`; confirms no mon command).
- `.../services/ceph_service.py:127-150` — `get_pool_list_with_stats` (the
  stateful stats path; out of scope).
- `third_party/ceph/src/pybind/mgr/dashboard/openapi.yaml` `/api/pool` GET
  — Accept version + param schema.
- `third_party/ceph/src/common/Formatter.cc` — bare inf/nan JSON tokens
  (sanitize before unmarshal).

### 11. Open decisions (boss)

- **`attrs` field-whitelist param (divergence):** it selects which
  attributes are emitted per pool. A typed/`Struct` response under the
  gateway's `EmitUnpopulated` marshaler **cannot** honor a dynamic
  field-subset. Decision: ignore `attrs` and always return the full pool
  object (documented divergence), OR honor it by post-filtering the
  `Struct` map keys in the handler before returning (feasible since each
  element is a `Struct` — the handler can delete non-whitelisted keys,
  keeping `pool_name`). Recommend honoring it via Struct-key filtering to
  preserve parity, since it is cheap with a `Struct` body; if not honored,
  record the divergence and parity must not send `attrs`.
- **`stats=true` (out of scope):** depends on mgr time-series
  (`pg_summary`, `get_updated_pool_stats`, rate computation) — the stateful
  mgr feature CLAUDE.md defers. Decision: return `ErrNotImplemented` when
  `stats` is true, and only implement the default (no-stats) path. Parity
  should exercise `stats=false`/omitted only.

### Implemented (resolution of §11)

- **Response modeling:** each pool is `google.protobuf.Struct`,
  `ListPoolsResponse{repeated Struct pools}` unwrapped via
  `response_body: "pools"` to a bare JSON array. Handler issues `osd dump`
  + `osd crush dump` and applies the four transforms in `serializePool`,
  with `sanitizeCephFloats` rewriting bare inf/-inf/nan tokens before
  unmarshal (inf→`"Infinity"` to match the dashboard read_balance rule).
- **`attrs`:** HONORED via Struct-key filtering in `serializePool`
  (`pool_name` always kept).
- **`stats=true`:** PARTIAL — returns `ErrNotImplemented`. Only the
  default no-stats path is implemented. Parity exercises stats omitted.
- Parity List uses an `attrs`-scoped read-back
  (`pool_name,type,size,crush_rule,application_metadata`) to diff the
  transform output across backends while avoiding volatile per-call fields
  (`last_change`, `read_balance` scores) that aren't a shape divergence.
- **`type`/`crush_rule` unmapped-value handling (documented divergence):**
  the dashboard does a bare Python dict index (`{1:'replicated',
  3:'erasure'}[pool[attr]]`, `crush_rules[pool[attr]]`, pool.py:111/113),
  which raises `KeyError` → 500 on an unknown type or a rule_id missing
  from the crush dump. The Go handler passes the raw value through instead
  (more robust). In practice Ceph only emits type 1/3 and rule_ids present
  in `osd crush dump`, so the wire never actually diverges; no api_diff
  entry needed.
- **`nan`/`-nan` read_balance scores (forced valid-JSON divergence):** the
  dashboard parses a bare `nan` into a real `float('nan')` and keeps it
  (only `isinf` floats become `"Infinity"`, pool.py:120). Go's
  `encoding/json` can neither parse nor emit a NaN float, so
  `sanitizeCephFloats` rewrites bare `nan`/`-nan` to the string `"NaN"` —
  the only valid-JSON option. `inf`/`-inf` still become `"Infinity"`
  (matches the dashboard). `read_balance` scores are excluded from the
  parity whitelist, so this does not flap parity; no api_diff entry needed.
  `sanitizeCephFloats` is now the single inf/nan mechanism (the former
  `convertReadBalanceInf` was redundant and was removed).

## Review

### Mechanical (M*)
- [x] M1: FIXED. `applicationMetadataKeys` now collects keys into a `[]string`, `sort.Strings` them, and returns the sorted list — deterministic output for the positional parity matcher. (pkg/api/pool_api_handlers.go) — SAME ISSUE as D3.

### Deep (D*)
- [x] D1: FIXED. Verified against the submodule: `JSONFormatter::dump_float` (Formatter.cc:330) streams the double via `std::ostream <<`, so libstdc++ emits negative NaN as bare `-nan`, which the old regex missed → whole-call failure. Widened the alternation to `([:\[,]\s*)(-?inf|-?nan)\b` so `-nan` is sanitized. Covered by a new `-nan` case in `Test_sanitizeCephFloats` and the end-to-end `Test_serializePool_readBalanceInf`.
- [x] D2: FIXED. Collapsed to one load-bearing mechanism: `sanitizeCephFloats` (pre-unmarshal byte rewrite) is now the sole inf/nan handler; `convertReadBalanceInf` and its dead `math.IsInf` branch are deleted, and read_balance is plain passthrough in `serializePool`.
- [x] D3: FIXED — same change as M1 (sorted keys).
- [x] D4: FIXED (documented divergence). Confirmed the dashboard does a bare Python dict index (`{1:'replicated',3:'erasure'}[pool[attr]]` / `crush_rules[pool[attr]]`, pool.py:111/113) → KeyError → 500 on unmapped values. We keep the more-robust passthrough and now document it in handler comments and §11. Ceph only ever emits type 1/3 and rule_ids that appear in the crush dump, so in practice this never diverges on the wire; no api_diff entry needed.
- [x] D5: FIXED (documented forced divergence). Confirmed the dashboard parses bare `nan` into a real Python `float('nan')` and, since `math.isinf(nan)` is False, keeps it as a float (pool.py:120). Go's `encoding/json` can neither parse nor emit a NaN float, so a real-NaN wire match is impossible; `"NaN"` is the only valid-JSON option. Documented in §11. read_balance scores are excluded from the parity whitelist, so it does not flap parity — no api_diff entry needed.
- [x] D6: FIXED. Added `Test_serializePool_readBalanceInf`: feeds raw `osd dump`-shaped bytes with a bare `inf` and `-nan` read_balance score through `sanitizeCephFloats` → unmarshal → `serializePool`, asserting `inf`→`"Infinity"`, `-nan`→`"NaN"`, finite passthrough, and a clean `structpb.NewStruct` round-trip.

### API-diff (A*)
(none — api_diff.yaml unchanged; reviewer self-exited)
