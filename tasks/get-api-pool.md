# Port ceph dashboard endpoint GET /api/pool

## Requirements

### 1. Summary
Lists all pools with their OSDMap properties. Maps to the dashboard
`Pool` RESTController `list()` method
(`third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:150`),
which delegates to `_pool_list` → `CephService.get_pool_list()` /
`get_pool_list_with_stats()`. Add a new `ListPools` rpc to the existing
`Pool` gRPC service.

### 2. Data source
`reconstruct`.

The dashboard does **not** issue any mon command for this route — it reads
the mgr's in-memory cached `osd_map`:
`get_pool_list()` returns `mgr.get('osd_map')['pools']`
(`.../services/ceph_service.py:120-125`). Confirmed live: a fresh
`GET /api/pool` (and `?stats=true`) produced **zero** dispatch lines in
`ceph.audit.log`.

We rebuild the same array from mon commands:

- **Base pool list** (`stats` absent/false): `osd dump` — its `pools[]`
  array is field-for-field the same object the dashboard serializes from
  `osd_map['pools']`. Verified: live `osd dump` `pools[0]` carries the
  identical keys (`pool`, `pool_name`, `type`, `crush_rule`,
  `application_metadata`, `read_balance`, `options`, `flags_names`,
  timestamps, …) that the dashboard response exposes.
- **Crush-rule id → name** shaping needs the rule table. Dashboard reads
  `mgr.get('osd_map_crush')['rules']` (`pool.py:104`); reconstruct with
  `osd crush dump` → `rules[].rule_id` / `rules[].rule_name` (this is
  exactly what `pkg/api/crush_rule_api_handlers.go` already does).

The `stats=true` path is a **documented divergence, not a hard-stop** —
see §11. The base list is the in-scope, fully reconstructable port; ship
that.

### 3. Scope & permission
First statement:
```go
if err := user.HasPermissions(ctx, user.ScopePool, user.PermRead); err != nil {
    return nil, err
}
```
Class decorator `@APIRouter('/pool', Scope.POOL)` (`pool.py:95`) →
`user.ScopePool`. `list()` has no explicit `@*Permission` decorator
(`pool.py:144-151`); `RESTController.list` is a standard method, so the
`GET→read` verb fallback applies (per `permissions.md` §Default
permission) → `user.PermRead`. No multi-scope, no dashboard-settings
gotcha.

### 4. Accept version
`application/vnd.ceph.api.v1.0+json` (`openapi.yaml`, `/api/pool` `get`).
Verified live: `v1.0` → 200, `v0.1` → 415.

### 5. Request
No path params, no body.

Query params (both optional):
- `attrs` **query** — `string` (CSV of attribute names). Whitelists which
  pool fields appear in each result object; `pool_name` is always forced
  in (`pool.py:128-129`). Empty/absent → all fields. This is a **dynamic
  response-shaping filter** — see §11.
- `stats` **query** — `bool` (default `false`, parsed via `str_to_bool`).
  When true, each pool gains a `stats` (and, for the with-stats path, a
  `pg_status`) sub-object. See §11.

### 6. Response
`200` → JSON **array** of pool objects. Each object mirrors `osd dump`
`pools[]` after these handler-side shaping transforms (all done in
`_serialize_pool`, `pool.py:99-130`):

- `type` **int→string**: `1→"replicated"`, `3→"erasure"` (`pool.py:110-111`).
  `osd dump` emits int `type`; the existing `OsdDumpPool.type` is `int32`,
  so a **pool-specific message with `string type`** is needed (see §8).
- `crush_rule` **id→name**: int rule id replaced by the rule's
  `rule_name` from `osd crush dump` (`pool.py:112-113`). Existing
  `OsdDumpPool.crush_rule` is `int32`; pool message needs `string crush_rule`.
- `application_metadata` **dict→list**: `osd dump` gives a dict
  (`{"rgw": {}}`); dashboard emits the **list of keys** (`["rgw"]`)
  (`pool.py:114-115`). Existing `OsdDumpPool` types it as `Struct`; pool
  message needs `repeated string application_metadata`.
- `read_balance` **inf→"Infinity"**: float `inf` values in the
  `read_balance` dict are replaced with the string `"Infinity"`
  (`pool.py:117-124`). Also relevant to the Ceph-formatter `inf`/`nan`
  quirk: `osd dump` can emit bare `inf`/`nan` tokens that Go's
  `encoding/json` rejects — sanitize raw bytes before unmarshal (same as
  the read-balance note in the ceph-src skill).

Concrete live sample (one element, `stats=false`, all attrs):
```json
{
  "pool": 1,                                  // pool id, int32
  "pool_name": ".rgw.root",                   // string (always present)
  "flags": 1,                                 // int64
  "flags_names": "hashpspool",                // string
  "type": "replicated",                       // shaped int->string
  "size": 1, "min_size": 1,                   // int32
  "crush_rule": "replicated_rule",            // shaped id->name (string)
  "object_hash": 2,
  "pg_autoscale_mode": "off",                 // string
  "pg_num": 8, "pg_placement_num": 8,
  "pg_placement_num_target": 8, "pg_num_target": 8, "pg_num_pending": 8,
  "last_pg_merge_meta": { "ready_epoch": 0, "last_epoch_started": 0,
    "last_epoch_clean": 0, "source_pgid": "0.0",
    "source_version": "0'0", "target_version": "0'0" },
  "auid": 0,                                  // uint64
  "snap_mode": "selfmanaged", "snap_seq": 0, "snap_epoch": 0,
  "pool_snaps": [],
  "quota_max_bytes": 0, "quota_max_objects": 0,   // uint64
  "tiers": [], "tier_of": -1, "read_tier": -1, "write_tier": -1,
  "cache_mode": "none",
  "target_max_bytes": 0, "target_max_objects": 0,
  "cache_target_dirty_ratio_micro": 400000,
  "cache_target_dirty_high_ratio_micro": 600000,
  "cache_target_full_ratio_micro": 800000,
  "cache_min_flush_age": 0, "cache_min_evict_age": 0,
  "erasure_code_profile": "",
  "hit_set_params": { "type": "none" },
  "hit_set_period": 0, "hit_set_count": 0, "use_gmt_hitset": true,
  "min_read_recency_for_promote": 0, "min_write_recency_for_promote": 0,
  "hit_set_grade_decay_rate": 0, "hit_set_search_last_n": 0,
  "grade_table": [],
  "stripe_width": 0, "expected_num_objects": 0, "fast_read": false,
  "options": {},                              // dict, free-form (Struct)
  "application_metadata": ["rgw"],            // shaped dict->list
  "read_balance": { "score_type": "Fair distribution",
    "score_acting": 1.0, "score_stable": 1.0, "optimal_score": 1.0,
    "raw_score_acting": 1.0, "raw_score_stable": 1.0,
    "primary_affinity_weighted": 1.0, "average_primary_affinity": 1.0,
    "average_primary_affinity_weighted": 1.0 },
  "create_time": "2026-05-31T15:25:04.647470+0000",  // google.protobuf.Timestamp
  "last_change": "9",                         // string (stringified epoch)
  "last_force_op_resend": "0",
  "last_force_op_resend_prenautilus": "0",
  "last_force_op_resend_preluminous": "0",
  "removed_snaps": "[]"                        // string, not array
}
```
Note `flags` is `int64` (existing `OsdDumpPool.flags`); `quota_*`,
`auid`, `target_max_*`, cache/hitset counters are `uint64`. The live
container also returns `peering_crush_bucket_*` and `is_stretch_pool`
keys (present in `osd dump`, absent from the openapi schema — openapi is
incomplete here; `OsdDumpPool` already has the `peering_crush_bucket_*`
fields). `create_time` is an ISO8601 timestamp.

Errors: `401` no token, `403` no `pool:read`. There are no per-request
4xx for `list` (bad `attrs` values are silently dropped).

### 7. Underlying mechanism
Two read commands, no mutation, no inbuf:
- `{"prefix": "osd dump", "format": "json"}` — pool array source.
- `{"prefix": "osd crush dump", "format": "json"}` — rule id→name map.

Audit-confirmed the dashboard itself fires **neither** (it reads mgr
cache); these are the ceph-api reconstruction commands, mirroring the
already-ported `GetCephOsdDump` (`status_api_handlers.go:113`) and
`ListRules` (`crush_rule_api_handlers.go:130`). Both are pure reads, so
nothing lands in `ceph.audit.log` as a mutation.

### 8. gRPC service decision
Extend the **existing `Pool` service** in `api/pool.proto` (per the task
note — `CreatePool` was just added). Add:
```proto
rpc ListPools (ListPoolsRequest) returns (ListPoolsResponse) {}
```
with `ListPoolsRequest { optional string attrs = 1; optional bool stats = 2; }`
and `ListPoolsResponse { repeated Pool pools = 1; }`.

Define a **new `Pool` message** in `pool.proto` rather than reusing
`status.proto`'s `OsdDumpPool`: three fields differ in wire type after
the dashboard's shaping — `type` (string not int32), `crush_rule` (string
not int32), `application_metadata` (`repeated string` not `Struct`). Copy
the remaining ~55 fields from `OsdDumpPool` verbatim (same json tags,
`int64 flags`, `google.protobuf.Timestamp create_time`, `Struct options`,
the `OsdDumpReadBalance`/`OsdDumpLastPgMergeMeta`/`OsdDumpHitSetParams`
sub-messages can be reused as-is). The `attrs` whitelist cannot be
honored by a typed proto — see §11.

### 9. Closest in-repo analogue
Primary: `pkg/api/crush_rule_api_handlers.go` `ListRules`
(`crush_rule_api_handlers.go:126`) — same `reconstruct` class:
`pool:read`-style guard, single `osd crush dump`, unmarshal into typed
proto slice, return `repeated`. Its e2e/parity tests
`test/crush_rule_api_test.go` + `test/crush_rule_parity_test.go` are the
template for the new `Test_Parity_Pool_List`.

Secondary (for the pool struct + `osd dump` parse + timestamp/`inf`
handling): `pkg/api/status_api_handlers.go` `GetCephOsdDump` +
`convertToPbGetCephOsdDumpResponse` (`status_api_handlers.go:108,150`)
and the `OsdDumpPool` struct in `pkg/types/status.go:75-130`. Reuse this
parse path, then apply the three shaping transforms.

### 10. Relevant src files
- `third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:95` —
  `@APIRouter('/pool', Scope.POOL)` (scope).
- `.../controllers/pool.py:99-130` — `_serialize_pool` (the four shaping
  transforms: type, crush_rule, application_metadata, read_balance).
- `.../controllers/pool.py:132-151` — `_pool_list` + `list` (attrs/stats
  params, the read-permission verb fallback).
- `.../services/ceph_service.py:120-125` — `get_pool_list` reads
  `mgr.get('osd_map')['pools']` (no mon command).
- `.../services/ceph_service.py:127-151` — `get_pool_list_with_stats`
  (pg_summary + `get_updated_pool_stats` time-series; the `stats=true`
  divergence).
- `third_party/ceph/src/pybind/mgr/dashboard/openapi.yaml` `/api/pool`
  `get` — Accept `v1.0`, response schema, `attrs`/`stats` params.
- (in-repo) `pkg/types/status.go:75-130` — `OsdDumpPool` struct to copy.
- (in-repo) `api/status.proto:201` — `OsdDumpPool` message to clone.

### Open decisions (resolved — documented partial-port divergences)

This port is a **documented partial**, consistent with the POST /api/pool
deferrals:

1. **`stats=true` — DEFERRED, returns `ErrNotImplemented`.** Faithful stats
   require the mgr's in-memory PGMap time-series
   (`get_updated_pool_stats` / `get_time_series_rates`), a stateful poller
   that is out of scope per CLAUDE.md. When the `stats` query param is true
   `ListPools` returns `types.ErrNotImplemented` (HTTP 501). The base
   (`stats` absent/false) list is fully reconstructed and shipped.
2. **`attrs` query param — IGNORED, full object always returned.** A typed
   proto with `EmitUnpopulated` always emits the full field set, so the
   dashboard's per-field whitelist cannot be honored without post-marshal
   pruning. We ignore `attrs` and return the full pool object. This is a
   harmless superset: the dashboard client only ever drops fields, never
   adds them. The proto field exists (so the param is accepted) but has no
   effect. No post-marshal pruning is attempted.

**Auditability of these two divergences (D6):** neither is exercised by the
parity suite, which records only the base list (`stats` absent/false, no
`attrs`). The "harmless" claim for both rests on the absence of such a case:
an `?attrs=` parity case *would* fail (parity `Compare` reports the extra
non-whitelisted keys ceph-api emits), and a `stats=true` case would fail on
the 501-vs-200 status class. If either query shape is later put under
parity, expect a failure by design — these are documented partial-port gaps,
not parity bugs.

Additional shape notes discovered during implementation (see §Implementer
issues in `tasks/log.md`):
- `is_stretch_pool` (bool) is in every `osd dump` pool entry and the
  dashboard emits it, though it is absent from `openapi.yaml` and from
  `OsdDumpPool`. Added to the `PoolInfo` message and parsed on the wrapper.
- `create_time` parity: the dashboard forwards Ceph's `...+0000` timestamp
  form (numeric offset, no colon) where protojson emits `...Z`. Absorbed by
  a new string↔string timestamp coercion in the parity matcher
  (`coerceEqual`), not a yaml ignore.

### 11. Open decisions (original boss notes, retained)
1. **`stats=true` is not faithfully reproducible (documented divergence).**
   The dashboard's per-pool `stats` block is `{latest, rate, rates}` per
   metric, where `rate`/`rates` come from the mgr's in-memory PGMap
   time-series (`get_updated_pool_stats` + `get_time_series_rates`,
   `ceph_service.py:142-148`) — a stateful poller we don't have. The
   `latest` snapshot **is** reconstructable from `df detail`
   (`pools[].stats`: verified the field set matches exactly —
   `stored`, `objects`, `bytes_used`, `max_avail`, `rd`/`wr`, …), and
   `pg_status` from `pg dump` `by_pool`. Recommended: implement `attrs`/
   non-stats first; for `stats=true` either (a) return `rate:0`/`rates:[]`
   with a real `latest` from `df detail` (lossy on busy clusters, but
   matches an idle cluster), or (b) return `ErrNotImplemented` when
   `stats=true`. Building a faithful time-series poller is out of scope
   (a "new stateful component" per CLAUDE.md) — flag to boss, don't
   silently implement.
2. **`attrs` is a dynamic response-shaping param** — it whitelists which
   fields each object emits. A typed proto with `EmitUnpopulated` always
   emits the full field set, so per-field filtering cannot be honored
   without post-marshal pruning. Recommended divergence: ignore `attrs`
   and always return the full object (a superset — the dashboard client
   only ever drops fields, so a superset is harmless), OR document
   `attrs` as unsupported. Do **not** leave this for the implementer to
   trip over.

## Review

### Mechanical
Clean — no failures. All 8 checklist items pass (single endpoint, identity, permission guard ScopePool/PermRead, placement, parity test, framework not weakened, e2e test, pure logic extracted+tested). Embedding/JSON-shadowing of `read_balance`/`is_stretch_pool` on `poolListEntry` verified correct; `structKeys(nil)` → nil → `[]` matches dashboard empty metadata.

### Deep
- [x] D1: Fixed. `coerceEqual` string↔string branch now compares by exact instant (`at.Equal(bt)`); the 5s skew tolerance is retained only for the number↔string sequential-recording case. Removed the now-unused `withinSkew`, updated the comment, and added a `diff_test.go` case asserting a sub-tolerance 3s string↔string difference is a real diff. (`test/parity/diff.go`)
- [x] D2: Fixed. Added a WHY-comment on `sanitizeCephFloats` noting only read_balance is expected to carry non-finite floats in osd dump (the rewrite scope is broader than the dashboard's read_balance-only transform but harmless). (`pkg/api/pool_api_handlers.go`)
- [x] D3: Verified via ceph-src and fixed. `read_balance` scores are dumped through `JSONFormatter::dump_float` → `stringstream << double` (`src/common/Formatter.cc:330`), so `+inf`, `-inf`, and `nan` can all appear as bare tokens. The dashboard replaces scores with `math.isinf(value)` true by `"Infinity"` (`pool.py:117-124`) — `math.isinf` is sign-agnostic, so **both `+inf` and `-inf` map to `"Infinity"`** there, and `nan` is left untouched (leaks through as a bare token). My prior `-inf`→`"-Infinity"` therefore *diverged*; fixed to map both inf signs to `"Infinity"` and quote `nan` as `"NaN"` purely to let `encoding/json` accept the unmarshal. Updated `TestSanitizeCephFloats` accordingly.
- [x] D4: Fixed. Added a divergence comment at the `crush_rule` id→name lookup noting it emits `""` on a missing rule id vs the dashboard's KeyError→500, matching the existing `poolTypeName` divergence note. (`pkg/api/pool_api_handlers.go`)
- [x] D5: Fixed. Added `TestSerializePool_AllFieldsCopied`, which unmarshals a fully-populated osd-dump entry (distinct non-zero value per scalar) and asserts every `PoolInfo` field round-trips, so a future copy omission is caught by a unit test rather than only the parity body diff. (`pkg/api/pool_api_handlers_test.go`)
- [x] D6: Recorded. Added the `attrs` (ignored→superset) and `stats=true`→`ErrNotImplemented` divergences, and the fact that neither is parity-tested, to the task §Open decisions and the `tasks/log.md` GET section.
- [x] D7: Dismissed (note only, no change). The `pool:read` guard is correctly derived from `@APIRouter('/pool', Scope.POOL)` + GET→read; the `ListRules` `osd:read` template difference is just a cross-reference caveat, not a bug.
- [x] D8: Dismissed (note only, no change). `CephTimestamp.UnmarshalJSON`'s narration comments are pre-existing debt in `pkg/types/status.go`, not introduced by this port; left as-is per guidance.
