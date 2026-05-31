# Port ceph dashboard endpoint GET /api/pool

## Requirements

### Scope & permission
- Controller `@APIRouter('/pool', Scope.POOL)` → `user.ScopePool`
  (`third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:95`).
- `list()` is a `RESTController` standard verb with **no** explicit
  `@*Permission` decorator (pool.py:144-151), so it falls back to the
  HTTP verb default `GET → read` → `user.PermRead`.
- First handler statement:
  `if err := user.HasPermissions(ctx, user.ScopePool, user.PermRead); err != nil { return nil, err }`.
- No `dashboard-settings` / multi-scope gotcha here.

### Required Accept header
- `application/vnd.ceph.api.v1.0+json` (openapi.yaml:9914). Without it the
  dashboard returns 415. (Parity recorder sets this from the route.)

### Request
- Method/path: `GET /api/pool`. No path params, no body.
- Query params (both optional, openapi.yaml:9898-9910):
  - `attrs` (string): comma-separated whitelist of pool attribute names to
    return. When set, only those attrs are emitted (`pool_name` is always
    added). When unset/empty → all attrs. pool.py:133-135, `_serialize_pool`
    pool.py:100-130.
  - `stats` (bool, default `false`): when true, augment each pool with
    `pg_status` + `stats`. pool.py:137-141. Parsed by `str_to_bool` so
    accepts `"true"/"1"/etc`.
- Proto request message (new `ListPoolsRequest`): `optional string attrs`,
  `optional bool stats`. Both become query params via `http.yaml` (no path
  binding, no body). Use proto3 `optional` so unset attrs → "return all".

### Underlying mechanism (IMPORTANT — diverges from dashboard internals)
- The dashboard issues **NO mon command**. Confirmed: `ceph.audit.log` was
  empty during a live `GET /api/pool?stats=true` call. It reads mgr's
  in-process cached cluster maps via `mgr.get(...)`:
  - base list: `CephService.get_pool_list()` → `mgr.get('osd_map')['pools']`
    (ceph_service.py:120-125).
  - crush-rule name resolution: `mgr.get('osd_map_crush')['rules']`
    (pool.py:104).
  - stats: `mgr.get('pg_summary')['by_pool']` +
    `mgr.get_updated_pool_stats()` (ceph_service.py:128-151).
- ceph-api has no mgr cache access; it must reconstruct the same data from
  mon commands through `rados.Svc.ExecMon`:
  - **`{"prefix":"osd dump","format":"json"}`** → `.pools[]` is the exact
    raw source of `mgr.get('osd_map')['pools']`. Already modeled as
    `types.CephOsdDumpResponse` / `pb.OsdDumpPool`
    (`pkg/api/status_api_handlers.go:108-127,150-217`,
    `api/status.proto:201-272`). Reuse the unmarshal type.
  - **`{"prefix":"osd crush dump","format":"json"}`** → `.rules[]` with
    `rule_id`/`rule_name` to build the id→name map for `crush_rule`
    serialization. Already used by the crush_rule handler
    (`pkg/api/crush_rule_api_handlers.go:106,130`).

### Response — base list (stats=false)
- HTTP body is a **bare JSON array** of pool objects (no wrapper). Use
  `response_body` unwrap in `http.yaml` so the array is the body, matching
  the dashboard. Proto: `ListPoolsResponse { repeated PoolListItem pools = 1; }`
  with `response_body: "pools"`.
- Each item is the raw `osd dump` pool object with three dashboard
  transforms applied in `_serialize_pool` (pool.py:100-130):
  - `type`: raw int → string. `{1:"replicated", 3:"erasure"}` (pool.py:111).
  - `crush_rule`: raw int id → rule **name** via the crush-dump map
    (pool.py:112-113).
  - `application_metadata`: raw dict `{"rgw":{}}` → list of keys `["rgw"]`
    (pool.py:114-115).
  - `read_balance`: `Infinity` floats stringified to `"Infinity"`
    (pool.py:117-124) — edge case only when score is infinite.
- Live response confirmed (full real shape recorded below). Concrete shape
  with proto-type notes:

```jsonc
{
  "pool": 1,                       // int32  (pool id)
  "pool_name": ".rgw.root",        // string (always present)
  "flags": 1,                      // int64  (osd dump emits 64-bit flag mask; OsdDumpPool.flags is int64)
  "flags_names": "hashpspool",     // string
  "type": "replicated",            // string  <-- transformed from int (1|3)
  "size": 1,                       // int32
  "min_size": 1,                   // int32
  "crush_rule": "replicated_rule", // string  <-- transformed from int id
  "peering_crush_bucket_count": 0,            // int32
  "peering_crush_bucket_target": 0,           // int32
  "peering_crush_bucket_barrier": 0,          // int32
  "peering_crush_bucket_mandatory_member": 2147483647, // int32 (sentinel)
  "is_stretch_pool": false,        // bool  (NOT in OsdDumpPool proto / POOL_SCHEMA — present on wire, see divergences)
  "object_hash": 2,                // int32
  "pg_autoscale_mode": "off",      // string
  "pg_num": 8,                     // int32
  "pg_placement_num": 8,           // int32
  "pg_placement_num_target": 8,    // int32
  "pg_num_target": 8,              // int32
  "pg_num_pending": 8,             // int32
  "last_pg_merge_meta": {          // object (OsdDumpLastPgMergeMeta)
    "ready_epoch": 0,              // int32
    "last_epoch_started": 0,       // int32
    "last_epoch_clean": 0,         // int32
    "source_pgid": "0.0",          // string
    "source_version": "0'0",       // string
    "target_version": "0'0"        // string
  },
  "auid": 0,                       // uint64
  "snap_mode": "selfmanaged",      // string
  "snap_seq": 0,                   // uint64
  "snap_epoch": 0,                 // uint64
  "pool_snaps": [],                // array (google.protobuf.Value list)
  "quota_max_bytes": 0,            // uint64
  "quota_max_objects": 0,          // uint64
  "tiers": [],                     // repeated int32
  "tier_of": -1,                   // int32 (signed; -1 = none)
  "read_tier": -1,                 // int32 (signed)
  "write_tier": -1,                // int32 (signed)
  "cache_mode": "none",            // string
  "target_max_bytes": 0,           // uint64
  "target_max_objects": 0,         // uint64
  "cache_target_dirty_ratio_micro": 400000,       // uint64
  "cache_target_dirty_high_ratio_micro": 600000,  // uint64
  "cache_target_full_ratio_micro": 800000,        // uint64
  "cache_min_flush_age": 0,        // uint64
  "cache_min_evict_age": 0,        // uint64
  "erasure_code_profile": "",      // string
  "hit_set_params": { "type": "none" },  // object (OsdDumpHitSetParams)
  "hit_set_period": 0,             // uint64
  "hit_set_count": 0,              // uint64
  "use_gmt_hitset": true,          // bool
  "min_read_recency_for_promote": 0,   // uint64
  "min_write_recency_for_promote": 0,  // uint64
  "hit_set_grade_decay_rate": 0,   // uint64
  "hit_set_search_last_n": 0,      // uint64
  "grade_table": [],               // array (google.protobuf.Value list)
  "stripe_width": 0,               // uint64
  "expected_num_objects": 0,       // uint64
  "fast_read": false,              // bool
  "options": {},                   // object (google.protobuf.Struct; e.g. pg_num_min/pg_num_max)
  "application_metadata": ["rgw"], // array of strings  <-- transformed from dict
  "read_balance": {                // object (OsdDumpReadBalance) — see score_type divergence
    "score_type": "Fair distribution",  // string  (NOT in OsdDumpReadBalance proto)
    "score_acting": 1.0,                 // double
    "score_stable": 1.0,                 // double
    "optimal_score": 1.0,                // double
    "raw_score_acting": 1.0,             // double
    "raw_score_stable": 1.0,             // double
    "primary_affinity_weighted": 1.0,    // double
    "average_primary_affinity": 1.0,     // double
    "average_primary_affinity_weighted": 1.0 // double
  },
  "create_time": "2026-05-31T11:19:41.510249+0000", // string ts; OsdDumpPool models as google.protobuf.Timestamp (parity coerces RFC3339<->unix)
  "last_change": "9",              // string (numeric-as-string on wire)
  "last_force_op_resend": "0",                 // string
  "last_force_op_resend_prenautilus": "0",     // string
  "last_force_op_resend_preluminous": "0",     // string
  "removed_snaps": "[]"            // string (literal stringified array)
}
```

- POOL_SCHEMA (pool.py:19-88) is the documented field set but is **not
  authoritative** — the live wire object has more fields (e.g.
  `peering_crush_bucket_*`, `is_stretch_pool`, `read_balance`,
  `snap_mode`) coming straight from `osd dump`. Mirror the live shape, not
  the schema.

### Response — stats=true additions
Each pool additionally gets (pool.py:137-141 → ceph_service.py:128-151):

```jsonc
"pg_status": { "active+clean": 8 },   // map<string,int32>: pg-state-string -> count, from mgr.get('pg_summary')['by_pool'][poolid]
"stats": {                            // map<string, {latest,rate,rates}> from mgr.get_updated_pool_stats()
  "stored":      { "latest": 1613, "rate": 0.0, "rates": [] },
  "objects":     { "latest": 6,    "rate": 0.0, "rates": [] },
  "bytes_used":  { "latest": 24576,"rate": 0.0, "rates": [] },
  "percent_used":{ "latest": 0.000012215, "rate": 0.0, "rates": [] },
  "max_avail":   { "latest": 2011900288, "rate": 0.0, "rates": [] }
  // full key set (~24): stored, stored_data, stored_omap, objects, kb_used,
  // bytes_used, data_bytes_used, omap_bytes_used, percent_used, max_avail,
  // quota_objects, quota_bytes, dirty, rd, rd_bytes, wr, wr_bytes,
  // compress_bytes_used, compress_under_bytes, stored_raw, avail_raw
}
```
- `latest` is `int64`/`double` per metric (`percent_used` is float; byte/obj
  counts are int64). `rate` is `double`. `rates` is a `repeated [unixSec,double]`
  time-series (empty on a quiet cluster).

**stats=true is NOT fully reproducible from a single mon command — flag for
implementer:**
- `stats[*].latest` values map to **`{"prefix":"df","format":"json","detail":"detail"}`**
  → `.pools[].stats` (same metric names; verified live). That covers `latest`.
- `stats[*].rate` and `stats[*].rates` come from mgr's historical counter
  cache (`get_time_series_rates`) and have **no mon-command equivalent** —
  reasonable port: emit `rate: 0` and `rates: []` (matches a quiet cluster;
  the dashboard itself returns empties when no history).
- `pg_status` (`pg_summary.by_pool`) has no direct mon command; it can be
  derived by aggregating `{"prefix":"pg dump","format":"json"}`
  `.pg_map.pg_stats[]` grouped by pool-id prefix and counting `.state`
  strings. Non-trivial; confirm scope with the user before implementing the
  stats branch. Consider porting `stats=false` first.

### Target gRPC service
- Extend the **existing `Pool` service in `api/pool.proto`** (created by the
  POST port). Add `rpc ListPools (ListPoolsRequest) returns (ListPoolsResponse)`.
  Implement in `pkg/api/pool_api_handlers.go` (`poolAPI`). New rpc on an
  existing service → only proto + `http.yaml` + handler (anatomy §5).

### Wire-shape divergences to handle (not auto-coerced)
- `type`, `crush_rule`, `application_metadata`: require active
  transformation in the handler (int→enum-string, id→name, dict→list). Not a
  parity coercion — must be coded.
- `read_balance.score_type` (string) and top-level `is_stretch_pool` (bool)
  appear on the live wire but are **absent from the reused `OsdDumpPool`
  proto** (`api/status.proto:201-296`). The new `PoolListItem` proto must add
  them (`string score_type` in read-balance; `bool is_stretch_pool`) or they
  drop from the response and fail parity.
- `last_change` etc. are numeric-as-string on the wire → model as `string`
  (matches `OsdDumpPool`), do not coerce to int.
- `create_time`: model as `google.protobuf.Timestamp` (parity coerces
  RFC3339 ↔ unix-seconds within skew, per parity README).
- `flags`: `int64` (osd dump flag mask is 64-bit; `OsdDumpPool.flags` is
  int64).

### Implementation files (src)
- `api/pool.proto` — add `ListPools` rpc + `ListPoolsRequest` /
  `ListPoolsResponse` / `PoolListItem` (+ stats sub-messages if porting
  stats).
- `api/http.yaml` — add `selector: ceph.Pool.ListPools`, `get: /api/pool`,
  `response_body: "pools"`.
- `pkg/api/pool_api_handlers.go` — add `ListPools`: perm check → `osd dump`
  + `osd crush dump` via `radosSvc.ExecMon` → transform → unwrap.
- `pkg/api/status_api_handlers.go` / `api/status.proto` — reference for the
  raw `osd dump` pool unmarshal type (`types.CephOsdDumpResponse`,
  `OsdDumpPool`) and the timestamp/Value handling pattern; reuse, don't
  re-derive.
- `pkg/api/crush_rule_api_handlers.go` — reference for `osd crush dump`
  unmarshal (`crushDump{ Rules []*pb.Rule }`) to build the id→name map.
- `pkg/types/` (cgo/!cgo) — `CephOsdDumpResponse` lives here (used by status
  handler); reuse for parsing.
- `test/pool_api_test.go` (+ `//go:build cgo`) and `test/pool_parity_test.go`
  — required by coverage gates (`AssertGRPCMethodsRouted`,
  `AssertRoutesCovered`).

### rados commands summary
- `ExecMon`, `{"prefix":"osd dump","format":"json"}` — pool list source.
- `ExecMon`, `{"prefix":"osd crush dump","format":"json"}` — crush id→name.
- (stats only) `ExecMon`, `{"prefix":"df","detail":"detail","format":"json"}`
  — per-pool `stats[*].latest`; `pg dump` for `pg_status` aggregation.

## Review

### Mechanical
- [x] M1: duplicate constant — collapsed `poolReadAccept`/`poolWriteAccept` into a single `poolAccept` constant in `test/pool_parity_test.go`.
- [x] M2: confirmed the string↔string coercion in `test/parity/diff.go:114-128` is part of the pre-existing parity framework (committed in 1638056/75923c5, not this endpoint's change) and is the minimal, generic shape-class coercion CLAUDE.md prescribes. `diff_test.go` covers it: `TestCompare_TimestampStringFormatsEqual` exercises offset-vs-Z match, beyond-tolerance diff, AND the "both strings, neither a time → literal compare" path (lines 92-98). No change needed.

### Deep
- [x] D1: `applicationMetadataKeys` now sorts the keys. Verified against Ceph source: `osd dump` serializes `application_metadata` from a `std::map<std::string,...>` (osd_types.h:1681), so its wire order is sorted ascending and the dashboard's `list(dict.keys())` preserves that order — sorting in Go reproduces the dashboard order exactly.
- [x] D2: `stats=true` now returns `ErrNotImplemented` (→ gRPC Unimplemented) instead of silently returning the base list; e2e test asserts this. `attrs` cannot be honored by a typed proto response (always emits all populated fields) — documented as an explicit intentional no-op in the handler.
- [x] D3: added `sanitizeNonFiniteFloats` over the raw `osd dump` bytes. Ceph's JSONFormatter streams non-finite doubles as the bare tokens `inf`/`-inf`/`nan` (Formatter.cc:292-298 via std::stringstream), which are invalid JSON and previously failed `json.Unmarshal` for the entire list. They are rewritten to `null` (→ 0 score). Residual edge-case divergence (dashboard emits the string `"Infinity"` because mgr hands it native Python floats, never JSON text) documented at the function; not added to api_diff.yaml because an ignore there would over-suppress healthy read_balance comparisons on every run, and the test cluster never produces infinite scores.
- [x] D4: missing crush rule id now returns `ErrInternal` rather than serializing to `""`, matching the dashboard's KeyError-on-miss behavior; the two non-atomic `ExecMon` calls make the transient race possible, so an explicit error is preferable to a silently wrong empty name.
- [x] D5: see M2 — `diff_test.go` covers the "both strings, neither a time → literal compare" path (`TestCompare_TimestampStringFormatsEqual`, lines 92-98); the branch is the minimal generalization (returns false,false unless both strings parse as Ceph/RFC3339 time).
- [x] D6: consolidated — added `score_type` to the shared `OsdDumpReadBalance` (`api/status.proto`) and dropped the duplicate `PoolReadBalance`; `PoolListItem.read_balance` now reuses `OsdDumpReadBalance`. The status endpoints are ours-only in parity (no dashboard body comparison), so adding the field there is safe.
