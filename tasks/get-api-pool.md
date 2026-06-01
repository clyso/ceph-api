# Port ceph dashboard endpoint GET /api/pool

## Requirements

### 1. Summary
Lists all pools with their full OSDMap properties, optionally filtered to a
subset of attributes (`attrs`) and optionally enriched with usage stats
(`stats`). Maps to the dashboard `Pool` controller's `list` method
(`RESTController.list`), `third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:150`.

### 2. Data source
`reconstruct`. The dashboard does **not** issue a mon command for this route
— `CephService.get_pool_list` reads `mgr.get('osd_map')['pools']`
(`services/ceph_service.py:120-123`), the mgr's cached OSDMap. Verified
against the live `ceph.audit.log`: a `GET /api/pool` completes in 0.002s and
fires no `osd dump`/pool command.

ceph-api rebuilds the same data from mon commands:

- **`osd dump`** (`{"prefix":"osd dump","format":"json"}`) → `.pools[]`. This
  array is field-for-field identical to `mgr.get('osd_map')['pools']`
  (verified: same 62 keys, same raw values). This is the primary source.
- **`osd crush dump`** (`{"prefix":"osd crush dump","format":"json"}`) →
  `.rules[]` to build the `rule_id → rule_name` map for the `crush_rule`
  transform (`_serialize_pool`, `pool.py:104,112-113`). The dashboard reads
  this from `mgr.get('osd_map_crush')['rules']`; `osd crush dump` carries the
  same `rules` array (already used this way by the in-repo crush_rule
  handler).

The handler then applies `_serialize_pool` (pool.py:99-130) transforms — see §6.

The `stats=true` branch is a separate, non-reconstructible component — see §11.

### 3. Scope & permission
First statement:
```go
if err := user.HasPermissions(ctx, user.ScopePool, user.PermRead); err != nil {
    return nil, err
}
```
Derived from `@APIRouter('/pool', Scope.POOL)` (pool.py:95) → scope string
`"pool"` → `user.ScopePool` (`pkg/user/system_roles.go:54`). `list` has **no**
explicit `@*Permission` decorator → `RESTController` GET-verb fallback →
`read` → `user.PermRead` (`pkg/user/system_roles.go:79`). No multi-scope
gotcha.

### 4. Accept version
`application/vnd.ceph.api.v1.0+json` (openapi.yaml:9914). Verified live:
`v0.1` → 415, `v1.0` → 200.

### 5. Request
No path or body params. Two **query** params (both optional):

- `attrs` — **query**, `string` (proto `string`, optional). CSV list of pool
  attribute names. Whitelists which fields are emitted per pool. Empty/absent
  → all attributes. Split on `,` (pool.py:134-135). **Response-shaping
  param** — see §11.
- `stats` — **query**, `bool` (proto `bool`, default `false`). Parsed via
  `str_to_bool` (accepts `"true"`/`"1"`/etc.). When true, enrich each pool
  with `pg_status` + `stats`. See §11.

No `**kwargs` flat-body hazard here (GET, no body).

### 6. Response
HTTP 200, JSON **array** of pool objects (proto: a `repeated` message in a
list-wrapper, or stream — match the crush_rule `ListRulesResponse` pattern).
Live sample of one element (full `attrs`, no stats) is the field set below.
All scalar ints are non-negative pool counters except where noted; quotas and
byte fields can exceed 2^31 → model as **`int64`**.

Top-level fields straight from `osd dump` `.pools[i]` (representative subset;
full key list is the 62 keys in pool.py:19-88 `POOL_SCHEMA` plus newer keys
`peering_crush_bucket_*`, `is_stretch_pool`, `read_balance` present live):

- `pool` int64 (pool id), `pool_name` string, `size` int32, `min_size` int32,
  `object_hash` int32, `pg_num` int64, `pg_placement_num` int64,
  `pg_num_target` int64, `pg_placement_num_target` int64, `pg_num_pending`
  int64, `flags` int64, `flags_names` string, `pg_autoscale_mode` string,
  `auid` int64, `snap_mode` string, `snap_seq` int64, `snap_epoch` int64,
  `quota_max_bytes` **int64**, `quota_max_objects` **int64**,
  `target_max_bytes` int64, `target_max_objects` int64, `cache_mode` string,
  `cache_target_*_micro` int64, `cache_min_flush_age`/`cache_min_evict_age`
  int64, `erasure_code_profile` string, `hit_set_*` int64, `use_gmt_hitset`
  bool, `min_*_recency_for_promote` int64, `stripe_width` int64,
  `expected_num_objects` int64, `fast_read` bool, `tier_of`/`read_tier`/
  `write_tier` int32 (can be `-1`), `peering_crush_bucket_*` int64
  (`peering_crush_bucket_mandatory_member` observed `2147483647` → int64),
  `is_stretch_pool` bool.
- `pool_snaps` array, `tiers` array<string>, `grade_table` array<string>.
- `last_pg_merge_meta` object: `ready_epoch`/`last_epoch_started`/
  `last_epoch_clean` int64, `source_pgid`/`source_version`/`target_version`
  string.
- `hit_set_params` object `{ "type": string }`.
- `options` object (free-form; live `{}`, schema notes `pg_num_min`/
  `pg_num_max` ints) → model as `google.protobuf.Struct`.
- `last_change` **string** (`"9"`), `last_force_op_resend*` **string**,
  `removed_snaps` **string** (`"[]"` — a string, not an array). `create_time`
  string (`"2026-06-01T13:51:55.326188+0000"` — keep as string; NOT a proto
  Timestamp, dashboard emits the raw OSDMap string).

**Handler-coded transforms** (`_serialize_pool`, pool.py:107-129) — these are
NOT free; the matcher won't synthesize them:
- `type`: `osd dump` int → string. `1→"replicated"`, `3→"erasure"`
  (pool.py:111). Model `type` as `string` in proto, convert in handler.
- `crush_rule`: `osd dump` int rule_id → rule_name string via the
  `osd crush dump` rule map (pool.py:112-113). Model as `string`.
- `application_metadata`: `osd dump` gives a dict (`{"rgw":{}}`); dashboard
  emits a **list of keys** (`["rgw"]`) (pool.py:114-115). Model as
  `repeated string`; in handler take map keys.
- `read_balance`: float `inf`/`-inf` values → the string `"Infinity"`
  (pool.py:117-124). Live values are finite floats. Per the ceph-src skill,
  also sanitize bare `inf`/`nan` tokens in raw `osd dump` bytes before
  unmarshalling (Go json rejects them). `read_balance` is mixed-type
  (`score_type` string + float scores) → model as `google.protobuf.Struct`.
- `pool_name` is force-included even when `attrs` excludes it
  (pool.py:128-129).

Statuses: 200 OK; 401 (no token), 403 (no `pool:read`). The `list` method has
no per-pool 404 (that's the `get` route, not this task).

### 7. Underlying mechanism
**No mon command is required for the data itself** — confirmed against
`ceph.audit.log` (the route reads the mgr OSDMap cache). ceph-api substitutes:

1. `osd dump` — `{"prefix":"osd dump","format":"json"}` via `rados.ExecMon`.
   Sample `.pools[0]`: `{"pool":1,"pool_name":".rgw.root","type":1,
   "crush_rule":0,"size":1,"application_metadata":{"rgw":{}},
   "quota_max_bytes":0,"last_change":"9","removed_snaps":"[]", ...}`. Defined
   in `MonCommands.h` (grep `"osd dump"`).
2. `osd crush dump` — `{"prefix":"osd crush dump","format":"json"}` — for the
   `rule_id→rule_name` map (`.rules[].rule_id`, `.rules[].rule_name`).

(`stats=true` would additionally need `df detail` plus a time-series cache —
see §11.)

### 8. gRPC service decision
Extend the existing **`api/pool.proto`** `service Pool` (currently only
`CreatePool`). Add `rpc ListPools(...) returns (...)`. Add the corresponding
`api/http.yaml` selector `get: /api/pool`.

### 9. Closest in-repo analogue
`pkg/api/crush_rule_api_handlers.go` — `ListRules`/`GetRule`
(`crush_rule_api_handlers.go:102-131`). Same `reconstruct` class: issues
`osd crush dump`, parses JSON, reshapes into a typed list response, guards
with `user.HasPermissions(ctx, user.ScopeOsd, user.PermRead)`. This pool
handler is the same pattern with `osd dump` as the source and the
`_serialize_pool` transforms layered on. Use its e2e + parity test
(`test/` crush rule list test) as the template.

### 10. Relevant src files
- `third_party/ceph/src/pybind/mgr/dashboard/controllers/pool.py:95` —
  `@APIRouter('/pool', Scope.POOL)` controller class.
- `pool.py:150-151` — `list` method (the route).
- `pool.py:132-142` — `_pool_list`: attrs split + stats branch.
- `pool.py:99-130` — `_serialize_pool`: the field transforms (§6).
- `services/ceph_service.py:120-125` — `get_pool_list` reads `mgr.get('osd_map')['pools']`.
- `services/ceph_service.py:127-151` — `get_pool_list_with_stats` (stats branch).
- `mgr_util.py:704-748` — `get_most_recent_rate` / `get_time_series_rates`
  (the rate math the time-series cache would need).
- `openapi.yaml:9896-9911` — route params + Accept v1.0.

### 11. Open decisions (RESOLVED in implementation)
- **`stats=true`** → resolved as option (a): handler returns
  `types.ErrNotImplemented` when `stats=true`. The no-stats reconstruct path
  is faithful; the rate/rates time-series is the flagged hard-stop-class
  stateful component and is not reimplemented.
- **`attrs`** → resolved as documented divergence: the param is accepted
  (proto field `attrs`) but not honored — a typed `repeated Struct` response
  under `EmitUnpopulated` always emits the full attribute set. No
  field-filtering. `pool_name` force-include is moot since all fields are
  always emitted. Covered by an e2e case asserting the full set returns
  regardless of `attrs`. **No `api_diff.yaml` entry was needed**: the parity
  test issues the dashboard's default request (no `attrs`), so both backends
  return the full body and diff clean.
- **`options` / `read_balance`** → each pool is modeled as
  `google.protobuf.Struct` (matching `CreatePool`'s body and tolerating the
  62+ version-drifting keys). The parity matcher compares both as decoded
  JSON objects, so Struct vs plain-object is transparent. `read_balance` inf
  tokens are sanitized to `"Infinity"` before unmarshal (verified live
  values are finite, so this is a safety path).

#### Original open decisions (boss)
- **`stats=true` is not faithfully reproducible (hard-stop-class subset).**
  `get_pool_list_with_stats` (ceph_service.py:127-151) attaches per-pool
  `pg_status` (from `mgr.get("pg_summary")['by_pool']`) and a `stats` map
  where each metric is `{latest, rate, rates}`. `latest` equals
  `ceph df detail` `.pools[].stats[name]` (verified identical). But `rate`
  and `rates` derive from `mgr.get_updated_pool_stats()` — an in-mgr,
  lifetime-accumulated **time series** of `(timestamp, value)` samples, which
  is exactly the new stateful poller/time-series component the CLAUDE.md goal
  flags as a hard-stop. **Decision needed:** implement the no-stats path now
  (faithful) and either (a) return `ErrNotImplemented` when `stats=true`, or
  (b) emit `latest` from `df detail` with `rate:0.0, rates:[]` as a
  documented lossy divergence. Recommend (a) for parity honesty.
- **`attrs` is a dynamic response-shaping query param.** It whitelists which
  fields are emitted per pool. A typed proto with `EmitUnpopulated` always
  emits the full field set, so a typed `ListPools` response cannot honor
  `attrs` filtering for free (per proto-grpc-gateway skill). **Decision:**
  document as a divergence and always return the full attribute set
  (ignoring `attrs`), OR post-filter the marshalled JSON in the handler.
  Note `pool_name` is always force-included regardless (pool.py:128-129).
- **`options` and `read_balance`** are free-form/mixed-type objects → modeled
  as `google.protobuf.Struct`; confirm the parity matcher tolerates Struct vs
  plain-object on the wire.

## Review

### Mechanical
No failures found.

### API-diff
No api_diff changes (reviewer self-exit).

### Deep
- [x] D1: `read_balance` inf→"Infinity" is applied globally by regex, diverging from the dashboard's per-key, read_balance-only, inf-not-nan rule. Dashboard `_serialize_pool` (pool.py:117-124) replaces `float('inf')` with `"Infinity"` ONLY inside the `read_balance` object and ONLY via `math.isinf` (does NOT touch `nan`). The Go `sanitizeCephJSON` regex (`pkg/api/pool_api_handlers.go:573,578`) rewrites every bare `inf`/`-inf`/`nan` token anywhere in the doc, and maps `nan`→`"Infinity"` too. So an `inf` outside read_balance is stringified by ceph-api but not by the dashboard; a `nan` anywhere becomes `"Infinity"` here vs dashboard emitting raw `NaN`. The byte-level approach silently broadens the dashboard's narrow rule. Consider scoping the substitution to read_balance, or at least not collapsing `nan`→`"Infinity"`. (live values are finite, so a thin edge)
  - Fixed the nan-vs-inf collapse: `sanitizeCephJSON` now maps `inf`/`-inf`→`"Infinity"` (matches the dashboard read_balance substitution) and `nan`→`"NaN"` (distinct, no longer masquerading as Infinity). The token still must be quoted at all so Go can unmarshal it (bare `inf`/`nan` from `Formatter.cc dump_float` are invalid JSON). Did not regex-scope to read_balance: that field is the only float source in `osd dump` and the per-key match is fragile vs the value-position regex; the meaningful divergence (nan→Infinity) is the one removed. Covered by `Test_sanitizeCephJSON_nanDistinctFromInf`.
- [x] D2: `application_metadata` keys are sorted, but the dashboard emits them in dict (insertion) order. `serializePool` (`pkg/api/pool_api_handlers.go:640`) sorts keys; dashboard does `list(pool[attr].keys())` (pool.py:115) — raw dict order from `osd dump` JSON. Parity matcher compares arrays order-significant, so a multi-app pool would diff if dashboard order isn't alphabetical. Sort was added for test determinism (test comment pool_api_handlers_test.go:699) but introduces a real ordering divergence. Match source order (preserve `osd dump` order). Current parity only asserts 2xx so won't catch it.
  - Fixed: `serializePools` now decodes each pool from `json.RawMessage` and recovers `application_metadata` key order from the raw bytes via `objectKeyOrder` (a token-stream decoder), since Go map decode loses order. `serializePool` emits keys in that source order instead of sorting. Covered by `Test_objectKeyOrder` and the updated multi-app transform test.
- [x] D3: `crush_rule`/`type` map-miss silently passes the raw int through, unlike the dashboard which 500s. In `serializePool` (`pkg/api/pool_api_handlers.go:625-633`) a `type` not in {1,3} or a `crush_rule` id absent from the rule-name map leaves the original integer. Dashboard does dict-lookup raising KeyError→500. Silent type divergence (int vs string) surfacing as body mismatch rather than clear error. Worth a deliberate decision/comment.
  - Fixed: `serializePool` now returns `types.ErrInternal` (→ 500) on an unmapped `type` or `crush_rule`, mirroring the dashboard's dict-lookup KeyError→500, instead of leaking the raw int into a string-typed field. Comment added at the function. Covered by `Test_serializePool_unknownMappingErrors`.
- [ ] D4: `stats` parsed as strict proto bool, dropping the dashboard's `str_to_bool` acceptance set. Dashboard runs `stats` through `str_to_bool` (accepts yes/no/1/0 etc.). grpc-gateway bool binding only accepts `strconv.ParseBool`. So `?stats=yes` is 400 here vs the NotImplemented path on dashboard. Low impact (stats unsupported anyway); flag as request-parsing divergence from typed bool.
  - Not applied (deliberate). `stats` in every form returns `ErrNotImplemented` (501) — the feature is unsupported. The only observable effect of the divergence is that the `str_to_bool`-only truthy strings (`yes`/`no`/`y`/`n`/`on`/`off`) yield 400 instead of 501; both are non-2xx for an unsupported branch. Degrading the well-typed proto `bool` to a `string` purely to align the rejection status of an unsupported feature contradicts the proto-quality rule (CLAUDE.md "Parity vs API quality"). `strconv.ParseBool` already covers `true/false/1/0/t/f` and their case variants, so the dashboard's documented default request diffs clean.
- [x] D5: Redundant duplicate parity Accept constant. `pool_parity_test.go:874-877` adds `poolReadAccept` identical to `poolWriteAccept`. Keep one shared `poolAccept` or inline. Minor style.
- [ ] D6: Informational — two sequential `ExecMon` calls (`osd dump` then `osd crush dump`), correctly aborts on either error; matches crush_rule analogue. No action beyond D3.
