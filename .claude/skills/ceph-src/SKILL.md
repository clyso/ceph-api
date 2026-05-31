---
name: ceph-src
description: Trigger when need ANY info about Ceph, any facts about Ceph, documentation, source code, anything regarding existing Ceph API, mon, mgr modules, dashboard. Use instead of web search or training data for ALL Ceph-related topics. Use to run and verify against real local ceph instance.
---

# ceph-src

`third_party/ceph` is the pinned upstream Ceph source. **Never derive
dashboard API shapes, command JSON, or permission scopes from training
data — they are version-specific and your training data is older.** Read
the submodule. When source-reading is ambiguous, probe a live dashboard
([verify.md](./verify.md)).

## Setup

```sh
make ceph-ref                       # one-time, after git clone
make ceph-ref-versions              # also fetches the extra tags for cross-release diffs
git -C third_party/ceph describe    # confirm the pinned ref
```

## Where to look (paths relative to `third_party/ceph/`)

| Question | Path |
|---|---|
| Dashboard handler for endpoint `X` | `src/pybind/mgr/dashboard/controllers/` — one file per resource. |
| Permission scope + per-method permission | `@APIRouter(..., Scope.X)` on the controller class + `@CreatePermission`/`@ReadPermission`/`@UpdatePermission`/`@DeletePermission` on each method. |
| Required `Accept` version for an endpoint | `src/pybind/mgr/dashboard/openapi.yaml`. Dashboard returns 415 without it. |
| Older `restful` mgr module handler | `src/pybind/mgr/restful/api/`. |
| Mon command JSON shape | `src/mon/MonCommands.h` — grep the command literal in the `COMMAND(...)` table. |
| Mgr command JSON shape | `src/mgr/MgrCommands.h` (table) + `src/mgr/DaemonServer.cc` (dispatch). Module-specific commands in `src/pybind/mgr/<module>/`. |

## Cross-release diff

Tags available are listed in the top-level Makefile's
`CEPH_REF_EXTRA_TAGS`.

```sh
git -C third_party/ceph diff <oldTag>..<currentTag> -- <path>
git -C third_party/ceph log  <currentTag>..<futureTag> -- <path>
```

## Broader navigation

For anything outside the ceph-api porting table above — RADOS internals,
CRUSH, RGW, RBD, CephFS, cephadm, config options, release notes,
docs-vs-code disagreements — see [repo-map.md](./repo-map.md). It
indexes `src/` by component, `doc/` by topic, and gives a
topic → starting-point table for the whole tree.

## Read-only

The submodule is reference. Never stage or commit changes inside
`third_party/ceph/` from this repo.
