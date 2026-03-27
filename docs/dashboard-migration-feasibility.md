# Dashboard API Migration Feasibility

This document maps every Ceph Dashboard REST endpoint group to an implementation strategy for **ceph-api**, specifying which go-ceph package or Ceph command covers it, what is already done, and where hard limitations exist.

---

## Current Implementation State

ceph-api today implements **5 gRPC services** over a single port (`:9969`) with cmux multiplexing HTTP/2 (gRPC) and HTTP/1.1 (REST via grpc-gateway), using **go-ceph v0.26.0** as its only Ceph dependency.

| Service | Proto | Backend |
|---------|-------|---------|
| **AuthService** | `auth.proto` | OAuth2 (ory/fosite), users in Ceph config-key store via RADOS |
| **UsersService** | `users.proto` | ceph-api users + RBAC (18 scopes × 4 ops, 7 built-in roles) stored in config-key |
| **ClusterService** | `cluster.proto` | Ceph auth (keyring) users via `mon_command(auth ls/add/rm/export/import)` + static config index (`pkg/cephconfig`) |
| **StatusService** | `status.proto` | `mon_command(status / mon dump / osd dump / pg dump / report)` |
| **CrushRuleService** | `crush_rule.proto` | `mon_command(osd crush dump / create-replicated / create-erasure / rm)` |

The RADOS connection (`pkg/rados/service.go`) exposes three command paths:
- `ExecMon()` → `conn.MonCommand()`
- `ExecMgr()` → `conn.MgrCommand()`
- `ExecMonWithInputBuff()` → `conn.MonCommandWithInputBuffer()`

---

## go-ceph v0.26.0 Package Capabilities

| Package | What it provides | Ceph operations |
|---------|-----------------|-----------------|
| `rados` | `Conn` with MonCommand, MgrCommand, PGCommand, OsdCommand; IoCtx for pool I/O | All mon/mgr/osd/pg commands; pool CRUD; object I/O; watch/notify; omap |
| `rbd` | Full image lifecycle | Create/Clone/Flatten/Copy/Remove/Rename/Resize; Snapshots (create/delete/list/rollback/protect/unprotect); Mirror (enable/disable/status, pool + image); Trash; Locks; Encryption (LUKS); Groups; Diff; Metadata |
| `rbd/admin` | RBD mirror schedules + tasks | MirrorSiteSchedule, TaskAdmin |
| `cephfs` | POSIX-like FS operations | Mount; MakeDirs/RmDir; Open/Read/Write/Seek/Truncate; Stat; Xattrs; directory listing |
| `cephfs/admin` | Volume/subvolume management via `MgrCommand` | CreateVolume/RemoveVolume; SubVolumeGroup CRUD; SubVolume CRUD/resize/path; Snapshot schedules; Clones; Mirror |
| `rgw/admin` | RGW HTTP Admin API client (HTTP, not RADOS) | User CRUD; access keys; subusers; caps; user quota; bucket list/info/remove/link/unlink; bucket quota; usage logs |

> **Note on `cephfs/admin`:** All operations use `MgrCommand` internally (e.g., `{"prefix":"fs volume create"}`). This requires the MGR daemon to be running and the `volumes` module to be enabled — both are standard in any production cluster. No in-process CPython required.

> **Note on `rgw/admin`:** This package wraps the RGW HTTP Admin Ops API using AWS Signature V4 authentication. It is an HTTP client to a running RGW daemon, not a RADOS operation. Endpoint discovery: `mon_command({"prefix":"service dump"})` → parse `services.rgw.daemons` for host:port.

---

## Full Dashboard API Feasibility Matrix

### Legend
- ✅ **Already implemented** in ceph-api
- 🟢 **Achievable with mon_command / mgr_command** (no new libraries, ~1–3 days)
- 🔵 **Achievable with go-ceph librbd** (new proto + handler, ~4–5 days)
- 🟣 **Achievable with go-ceph cephfs/admin** (new proto + handler, ~3–4 days)
- 🟠 **Achievable with go-ceph rgw/admin HTTP client** (new proto + handler + HTTP discovery, ~5–7 days)
- 🟡 **Partial**: some endpoints achievable, some not
- 🔴 **Blocked**: requires Orchestrator (cephadm/rook) or in-process MGR

---

### Auth `✅`
ceph-api has a full OAuth2 identity provider (ory/fosite) with password grant + refresh tokens. JWT auth is enforced on every RPC. This is a superset of the dashboard's session-cookie auth. **Complete.**

---

### Users `✅`
ceph-api manages its own users + roles in the Ceph config-key store (zero external DB). The RBAC engine covers 18 permission scopes × 4 operations with 7 built-in system roles and custom role support. **Complete.**

---

### Cluster (auth users + config search) `✅`
`ClusterService` already covers Ceph auth (keyring) user CRUD (`auth ls / add / rm / export / import`) and config parameter search via the embedded 900+ param index in `pkg/cephconfig`. **Complete.**

---

### Health `🟢`
Every piece of data the dashboard's `/health` endpoint returns maps to a direct `mon_command`:

| Dashboard data | mon_command |
|----------------|-------------|
| Cluster health | `{"prefix":"health","detail":"detail"}` |
| Disk usage | `{"prefix":"df","detail":"detail"}` |
| FS map | `{"prefix":"fs dump"}` |
| MGR map | `{"prefix":"mgr dump"}` |
| Monitor status | `{"prefix":"quorum_status"}` |
| OSD map | `{"prefix":"osd dump"}` |
| OSD tree | `{"prefix":"osd tree","format":"json"}` |
| CRUSH map | `{"prefix":"osd crush dump"}` |
| CRUSH text | `{"prefix":"osd getcrushmap"}` |
| OSD metadata | `{"prefix":"osd metadata"}` |
| Config FSID | `{"prefix":"status"}` → `.fsid` |

**No perf counters or Prometheus needed for the health summary.** Full replacement feasible.

---

### ClusterConfiguration `🟢`
- **Config read** (list + get + filter): `mon_command("config dump")` returns all runtime overrides; ceph-api's `pkg/cephconfig` index provides the schema (types, defaults, valid ranges) without any `mgr.get('config_options')` call.
- **Config write** (set + delete + bulk_set): `mon_command("config set / rm")`.
- **Module options**: Options backed by the config store are accessible via `mon_command("config get/set who=mgr.X")`. Options stored in the module's private KV store (`get_store` / `set_store`) have no external access path — this is a minor gap covering only a small subset of per-module settings.

---

### CrushRule `✅`
**Already implemented.** GET uses `mon_command("osd crush dump")` (not `mgr.get('osd_map_crush')` — the migration doc was inaccurate here). CREATE/DELETE use `mon_command`. **Complete.**

---

### ErasureCodeProfile `🟢`
- GET: `mon_command("osd dump")["erasure_code_profiles"]` — the dashboard uses `mgr.get('osd_map')` but the same data is in `osd dump`. The migration doc was wrong to say "no command detected."
- POST: `mon_command("osd erasure-code-profile set")`
- DELETE: `mon_command("osd erasure-code-profile rm")`

---

### OSD `🟡`
| Endpoint | Implementation | Status |
|----------|---------------|--------|
| `/osd` list (map + tree + state) | `mon_command("osd dump")` + `mon_command("osd tree")` | 🟢 |
| `/osd` list (per-OSD I/O rate stats) | `mgr.get_unlabeled_counter_latest("osd", ...)` | 🔴 — perf counter only; workaround: Prometheus |
| `/osd/flags` GET | `mon_command("osd dump")["flags_set"]` | 🟢 |
| `/osd/flags` SET | `mon_command("osd set / osd unset / osd set-group")` | 🟢 |
| `/osd/{id}` PUT (in/out/down/up) | `mon_command("osd in/out/down/up")` | 🟢 |
| `/osd/{id}` reweight | `mon_command("osd reweight")` | 🟢 |
| `/osd/{id}` scrub/deep-scrub | `mon_command("osd scrub / deep-scrub")` | 🟢 |
| `/osd/{id}` purge/destroy | `mon_command("osd purge-actual / osd destroy-actual")` | 🟢 |
| `/osd/safe_to_destroy` | `mon_command("osd safe-to-destroy")` — note: `target:("mgr","")` in dashboard, but the Mon also handles it | 🟢 |
| `/osd/safe_to_delete` | Uses Orchestrator `_check_delete()` | 🔴 — requires Orchestrator |
| `/osd/{id}/devices` | `mon_command("device ls-by-daemon")` | 🟢 |
| `/osd` deployment options | Orchestrator inventory | 🔴 |

---

### Pool `🟢`
| Operation | mon_command |
|-----------|-------------|
| List pools | `{"prefix":"osd dump"}` → `.pools[]` |
| Get pool | same + `{"prefix":"osd crush dump"}` for crush rules |
| Create pool | `{"prefix":"osd pool create"}` |
| Delete pool | `{"prefix":"osd pool delete"}` |
| Set pool options | `{"prefix":"osd pool set"}` |
| Set/get quota | `{"prefix":"osd pool set-quota / get-quota"}` |
| Application enable | `{"prefix":"osd pool application enable/disable/get"}` |
| Pool stats | `{"prefix":"df detail"}` → per-pool stats |

PG summary per pool: `mon_command("pg dump")["pool_stats"]`. No `mgr.get()` needed.

---

### RBD `🔵`
All operations use go-ceph's `rbd` package via `conn.OpenIOContext(poolName)`:

| Operation | go-ceph call |
|-----------|-------------|
| List images | `rbd.GetImageNames(ioctx)` |
| Create image | `rbd.CreateImage(ioctx, name, size, order, features)` |
| Clone image | `rbd.CloneImage(p_ioctx, parent, snap, c_ioctx, child, features)` |
| Delete image | `image.Remove()` or `image.MoveImageToTrash()` |
| Resize | `image.Resize(size)` |
| Image info | `image.Stat()` + `image.GetId()` + `image.GetFeatures()` |
| Snapshots | `image.CreateSnapshot(name)` / `image.RemoveSnapshot(name)` / `image.ListSnapshots()` / `image.RollbackToSnapshot(name)` |
| Protect/Unprotect | `snap.IsProtected()` / `snap.Protect()` / `snap.Unprotect()` |
| Mirror pool | `rbd.EnablePoolMirroring(ioctx, mode)` / `rbd.GetPoolMirroringInfo(ioctx)` |
| Mirror image | `image.GetMirroringInfo()` / `image.EnableMirroring(mode)` |
| Trash | `image.MoveImageToTrash(delay)` / `rbd.ListTrashEntries(ioctx)` (capped at 10,240 — paginate at handler level) |
| Locks | `image.ListLockers()` / `image.BreakLock(client, cookie)` |

**Known limitation:** `image.Read()`/`image.Write()` are not goroutine-safe; use `ReadAt()`/`WriteAt()` for concurrent access.

---

### RGW `🟠`
Use go-ceph's `rgw/admin` HTTP client. Discover RGW endpoint via `mon_command({"prefix":"service dump"})` → `services.rgw.daemons[*].metadata.frontend_config#0`.

| Operation | go-ceph rgw/admin call |
|-----------|----------------------|
| User CRUD | `api.GetUser()` / `api.CreateUser()` / `api.RemoveUser()` / `api.ModifyUser()` |
| List users | `api.GetUsers()` |
| Access keys | `api.CreateKey()` / `api.RemoveKey()` |
| Subusers | `api.CreateSubuser()` / `api.ModifySubuser()` / `api.RemoveSubuser()` |
| User caps | `api.AddCaps()` / `api.RemoveCaps()` |
| User quota | `api.GetUserQuota()` / `api.SetUserQuota()` |
| Bucket list | `api.ListBuckets()` |
| Bucket info/stats | `api.GetBucketInfo()` |
| Bucket policy | `api.GetBucketPolicy()` |
| Remove bucket | `api.RemoveBucket()` |
| Bucket quota | `api.GetBucketQuota()` / `api.SetBucketQuota()` |
| Link/Unlink bucket | `api.LinkBucket()` / `api.UnlinkBucket()` |
| Usage logs | `api.GetUsage()` / `api.TrimUsage()` |

**Not covered by go-ceph rgw/admin:** bucket notifications, lifecycle/CORS/SSE/replication, zone/zonegroup/realm management (these require custom HTTP calls to RGW Admin API or `mon_command` for zone topology).

Zone/realm management: `mon_command({"prefix":"rgw zone get/list/modify/create"})` covers basic zone operations.

---

### CephFS `🟣`
Use go-ceph's `cephfs/admin` package (`FSAdmin` backed by `MgrCommand`):

| Operation | go-ceph cephfs/admin call |
|-----------|--------------------------|
| List filesystems | `fsa.ListFileSystems()` / `fsa.EnumerateVolumes()` |
| Create volume | `fsa.ListVolumes()` + `fsa.marshalMgrCommand({"prefix":"fs volume create",...})` |
| Remove volume | `fsa.marshalMgrCommand({"prefix":"fs volume rm",...})` |
| Volume status | `fsa.VolumeStatus(name)` |
| SubVolumeGroup CRUD | `fsa.CreateSubVolumeGroup()` / `fsa.RemoveSubVolumeGroup()` / `fsa.ListSubVolumeGroups()` / `fsa.SubVolumeGroupInfo()` |
| SubVolume CRUD | `fsa.CreateSubVolume()` / `fsa.RemoveSubVolume()` / `fsa.ListSubVolumes()` / `fsa.SubVolumeInfo()` / `fsa.ResizeSubVolume()` |
| SubVolume path | `fsa.SubVolumePath()` |
| Snapshots | `fsa.CreateSubVolumeSnapshot()` / `fsa.RemoveSubVolumeSnapshot()` / `fsa.ListSubVolumeSnapshots()` |
| Clones | `fsa.CreateClone()` / `fsa.CloneStatus()` / `fsa.CancelClone()` |
| Mirror | `fsa.EnableMirror()` / `fsa.DisableMirror()` + peer management |
| Snapshot schedule | `fsa.AddSnapshotSchedule()` / `fsa.RemoveSnapshotSchedule()` / `fsa.ListSnapshotSchedules()` |
| FS map | `mon_command("fs dump")` |
| MDS session eviction | `conn.MdsCommand(fsName, 0, [][]byte{...})` via rados Conn |

**Perf counters for MDS** (`mgr.get_unlabeled_counter_latest("mds", ...)`) are not accessible — use Prometheus workaround.

---

### MGR Modules `🟢`
| Operation | Backend |
|-----------|---------|
| List modules + available | `mon_command("mgr dump")["modules"]` + `["available_modules"]` |
| Enable module | `mon_command("mgr module enable")` |
| Disable module | `mon_command("mgr module disable")` |
| Get module config options | `mon_command("config get who=mgr.X key=Y")` for config-store backed opts |
| Set module config options | `mon_command("config set who=mgr.X key=Y value=Z")` for config-store backed opts |

**Gap:** Module options stored in the module's private KV (`get_store`/`set_store`) cannot be read or set externally. These represent a small subset of module settings (e.g., per-session dashboard state).

---

### Logs `🟢`
```
mon_command({"prefix":"log last","num":100,"channel":"cluster","format":"json"})
```
Single call, trivially implementable.

---

### Monitor `🟢`
| Data | mon_command |
|------|-------------|
| Monitor list + status | `{"prefix":"mon dump"}` |
| Quorum status | `{"prefix":"quorum_status"}` |
| Monitor perf counters | Not available — use Prometheus at `:9283` |

---

### Prometheus / Alertmanager `🟢`
ceph-api can proxy HTTP requests to the Prometheus and Alertmanager endpoints. Discover addresses via `mon_command("mgr dump")["services"]["prometheus"]`. No go-ceph involvement — pure HTTP reverse proxy.

Prometheus scrape (`http://<mgr>:9283/metrics`) is also the recommended workaround for all per-daemon I/O rate metrics that the dashboard reads via `mgr.get_unlabeled_counter_latest()`.

---

### Perf Counters `🔴 (workaround available)`
The dashboard's `/perf_counters` endpoint uses `mgr.get_unlabeled_perf_schema()` and `mgr.get_unlabeled_counter_latest()`. These are perf counter circular buffers maintained exclusively inside the active MGR process — no external access path exists.

**Workaround:** If the MGR's Prometheus module is enabled (default), poll `http://<mgr>:9283/metrics`. All OSD, MDS, MON, RGW, and MGR perf counters are exposed as labeled Prometheus metrics. Expose a `/metrics/forward` endpoint in ceph-api that proxies or queries this.

---

### Daemon / Host / Hardware / Cluster Upgrade `🔴`
All four require the Orchestrator (cephadm or rook). There is no librados path to:
- Start/stop/restart individual daemons
- Add/remove hosts from the cluster
- Query hardware health (SMART data via Orchestrator)
- Coordinate rolling cluster upgrades

These are out of scope for the current go-ceph based approach. They require either implementing an Orchestrator gRPC client (e.g., against cephadm's service API) or a lightweight integration with the MGR orchestrator module commands: `mon_command("orch host ls")`, `mon_command("orch ls")`, etc., which return read-only state without the ability to mutate.

---

### iSCSI `🔴 (skip)`
The iSCSI gateway has been in maintenance mode since November 2022. It requires a custom HTTP client to the iSCSI gateway REST API and is not a priority.

---

### FeatureToggles `🔴 (not applicable)`
This is a Dashboard-internal feature flag system using the module's private KV store. Not relevant to ceph-api.

---

## Replacing `ceph` CLI Commands

ceph-api can serve as a full replacement for the `ceph` CLI for administrative operations, mapping every command group:

| CLI group | ceph-api implementation |
|-----------|------------------------|
| `ceph auth ...` | `ClusterService` (already done) |
| `ceph config ...` | `ClusterService.SearchConfig` + new Config RPC |
| `ceph osd pool ...` | New `PoolService` (mon_command) |
| `ceph osd crush rule ...` | `CrushRuleService` (already done) |
| `ceph osd ...` (mgmt) | New `OSDService` (mon_command) |
| `ceph status / health / df` | `StatusService` (already done) + new HealthService |
| `ceph pg dump / pg stat` | `StatusService.GetCephPgDump` (already done) |
| `ceph mon dump` | `StatusService.GetCephMonDump` (already done) |
| `ceph fs ...` | New `CephFSService` (cephfs/admin MgrCommand) |
| `ceph mgr module ...` | New `MgrModuleService` (mon_command) |
| `ceph log last` | New `LogService` (mon_command) |
| `ceph osd erasure-code-profile ...` | New `ErasureCodeService` (mon_command) |
| `ceph nfs ...` | New `NfsService` (mgr_command) |
| `rbd ...` | New `RBDService` (go-ceph librbd) |
| `radosgw-admin ...` | New `RGWService` (go-ceph rgw/admin HTTP) |
| `ceph tell osd.X ...` | `rados.Conn.OsdCommand(osdId, args)` — available in go-ceph |
| `ceph tell pg <pgid> query` | `rados.Conn.PGCommand(pgid, args)` — available in go-ceph |
| `ceph -w` (watch loop) | **Not replaceable** — requires Mon log streaming |

**Commands that cannot be replaced by ceph-api today:**
- `ceph -w` / `ceph status --watch` — real-time event streaming from Mon
- Live I/O rate display (`ceph iostat`) — requires perf counter access (use Prometheus instead)
- Direct object I/O (`rados put/get/bench`) — RADOS object operations (could be added as a new ObjectService using IoCtx, but not part of dashboard scope)

---

## Implementation Roadmap

| Priority | Service | Effort | Dependencies |
|----------|---------|--------|-------------|
| P0 (now) | **HealthService** | 2 days | mon_command only |
| P0 | **PoolService** | 3 days | mon_command only |
| P0 | **OSDService** | 3 days | mon_command only |
| P1 | **ErasureCodeService** | 1 day | mon_command only |
| P1 | **MgrModuleService** | 1 day | mon_command only |
| P1 | **LogService** | 0.5 day | mon_command only |
| P1 | **ConfigWriteService** | 1 day | mon_command only |
| P2 | **RBDService** | 5 days | go-ceph librbd (already in dep) |
| P2 | **CephFSService** | 4 days | go-ceph cephfs/admin (already in dep) |
| P2 | **RGWService** | 7 days | go-ceph rgw/admin HTTP (already in dep) |
| P3 | **MonitorService** | 1 day | mon_command only |
| P3 | **PrometheusProxy** | 2 days | HTTP proxy |
| P4 | **NfsService** | 2 days | mgr_command |
| Out of scope | Daemon/Host/Hardware | — | Orchestrator required |
| Out of scope | ClusterUpgrade | — | Orchestrator required |
| Out of scope | iSCSI | — | Deprecated |
| Not possible | Live perf counters | — | In-process MGR only; use Prometheus |

**Total to full dashboard parity (excluding Orchestrator and live perf):** ~37 developer-days.

---

## Permanent Limitations vs Dashboard

| Dashboard feature | Limitation | Workaround |
|-------------------|-----------|------------|
| Per-OSD I/O rate stats (ops/sec, bytes/sec) | `mgr.get_unlabeled_counter_latest("osd", ...)` — MGR-internal circular buffer | Poll Prometheus at `:9283` for `ceph_osd_op*` metrics |
| Per-MDS perf counters | Same — `mgr.get_unlabeled_counter_latest("mds", ...)` | Poll Prometheus |
| MGR module private KV options | `mgr.get_store()`/`set_store()` has no external access | Accept gap or proxy through a thin MGR shim module |
| Daemon start/stop/restart | Orchestrator only | Out of scope |
| Host add/remove | Orchestrator only | Out of scope |
| Hardware health (SMART) | Orchestrator only | Out of scope |
| OSD safe_to_delete | Orchestrator `_check_delete()` | `osd safe-to-destroy` (mon_command) covers the Ceph-level check |
| Bucket notifications | Not in RGW Admin API or go-ceph | Custom HTTP to RGW S3 API |
| S3 lifecycle/CORS/SSE/replication | Not in go-ceph rgw/admin | Custom HTTP to RGW Admin API |
