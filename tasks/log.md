
## Investigator issues for POST /api/pool

- `permissions.md` "Default permission" table and `anatomy.md` cover the
  clean single-command case (crush_rule). They give no guidance for a
  RESTController method that fans out into a *sequence* of mon commands
  plus non-mon-command side effects (librbd `pool_metadata_set` for
  `configuration`, an rbd-mirror command for `rbd_mirroring`). The "Data
  source" taxonomy in the boss template (`forwards-command` /
  `reconstruct` / `hard-stop`) has no clean slot for "mostly
  forwards-command but with two optional librbd/other-resource side
  channels" — I classified it `forwards-command` and pushed the side
  channels to §Open decisions, but the template could acknowledge this
  mixed case.
- `openapi.yaml` POST `/api/pool` request body is actively *wrong* (shows
  the `RBDPool` subclass's `{pool}` body, not `Pool.create`'s real
  signature). This is exactly the "swagger body can be wrong" quirk the
  ceph-src skill warns about; worth keeping as a canonical example.
- The dashboard's own behavior is internally inconsistent: missing
  required `pg_num` yields a 500 (Python `int(None)` crash) rather than a
  400, so faithful status-class parity is impossible without copying a
  bug. Logged as a documented divergence.

## Implementer issues for POST /api/pool

- §9 (closest analogue) and `anatomy.md` §Proto point at reusing the
  existing `PoolType` enum (defined in `api/crush_rule.proto` with values
  `replication`/`erasure`). For pool create the dashboard wire string is
  `"replicated"`, not `"replication"`, and `pool_type` is forwarded
  verbatim to `osd pool create` rather than branched on (unlike
  crush_rule, which switches the command prefix). I modeled `pool_type` as
  a plain `string` field (faithful verbatim proxy) instead of an enum —
  reusing the global enum would have either mismatched the wire string or
  required a second enum. Requirements §5/§7 were correct; only the
  analogue's enum modeling didn't transfer.

## Reviewer/fix issues for POST /api/pool

- The pool e2e + parity tests create real pools, which puts PGs through
  peering and populates `pg_stats[].avail_no_missing`. That surfaced a
  pre-existing latent bug in the *status* endpoint: `avail_no_missing` was
  typed `repeated int64` in `api/status.proto` (and `[]int64` in
  `pkg/types/status.go`), but Ceph renders it as JSON *strings* via
  `dump_stream` (a `pg_shard_t` array, `osd_types.cc` `PGStat::dump`). On a
  fresh cluster the array is empty so `Test_GetCephPgDump` /
  `Test_Parity_Status_PgDump` passed; adding pool-creating tests made the
  field non-empty and broke `make full-gate` with a JSON unmarshal error.
  Fixed by retyping to `repeated string`. Takeaway for the pipeline: a new
  endpoint's e2e/parity tests can perturb shared cluster state enough to
  expose latent typing bugs in *other* endpoints whose tests only ever ran
  against an idle cluster. `object_location_counts` (also `repeated int64`,
  dumped as objects) is the same class of latent bug but stayed empty, so it
  was left untouched as out of scope.

## Implementer issues for GET /api/pool

- §6/§8 Requirements listed the response fields from the openapi schema +
  `OsdDumpPool`, but the live dashboard also emits `is_stretch_pool` (bool)
  in every pool object. §6 noted `peering_crush_bucket_*` and
  `is_stretch_pool` are "present in osd dump, absent from the openapi
  schema" but only flagged the former as already-in-`OsdDumpPool`; it did
  not state `is_stretch_pool` needs to be *added* to the new message. The
  parity diff caught it (`missing @ $.N.is_stretch_pool`). Added
  `bool is_stretch_pool` to `PoolInfo` (`api/pool.proto`) and parsed it on
  the `poolListEntry` wrapper in `pkg/api/pool_api_handlers.go`.
- The `create_time` parity divergence was not anticipated: the dashboard
  forwards Ceph's `...+0000` timestamp string (numeric offset, no colon)
  while protojson emits `...Z` for the same instant. The existing
  `coerceEqual` (`test/parity/diff.go`) only coerced timestamp-string ↔
  unix-number, and was only invoked on *type* mismatches, so two
  same-instant timestamp *strings* fell through to a plain value diff. Added
  a string↔string timestamp coercion (parses both RFC3339Nano and Ceph's
  numeric-offset layout) backed by a `diff_test.go` case, per the parity
  README's sanctioned alternative to a yaml ignore. `OsdDumpPool` itself has
  the same `create_time`, but its `/api/status/osd_dump` route isn't in the
  dashboard openapi so it is never cross-diffed — this is the first endpoint
  to surface the timestamp-format divergence.
- Two query-param divergences are unverified at the parity layer (no parity
  case exercises them, so the "harmless" claim rests on no such case
  existing): (a) `attrs` is ignored and the full pool object is always
  returned — an `?attrs=` parity case would fail because `Compare` reports
  the extra (non-whitelisted) keys; (b) `stats=true` returns
  `ErrNotImplemented` (501) where the dashboard returns 200 with a `stats`
  sub-object. Both are recorded in the task §Open decisions; the parity
  suite only covers the base (`stats` absent/false, no `attrs`) list.
