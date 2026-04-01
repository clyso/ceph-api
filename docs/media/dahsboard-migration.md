# ceph-api: Standalone Container Feasibility

The Ceph Dashboard is an MGR module and some of its endpoints read from **MGR in-process memory** (perf counter circular buffers, live daemon registry). These have no network-accessible equivalent. This document identifies which endpoints are affected and whether there are workarounds.

---

## Example: `GET /api/osd` — perf counter circular buffers

The OSD list endpoint shows per-OSD PG count and used bytes. The dashboard reads these from MGR's in-RAM perf counter buffers, populated by OSDs reporting to the MGR daemon over the mgr wire protocol. There is no `mon_command`, RADOS object, or HTTP route that exposes this data.

[`src/pybind/mgr/dashboard/controllers/osd.py:190`](https://github.com/ceph/ceph/blob/v20.3.0/src/pybind/mgr/dashboard/controllers/osd.py#L190)

```python
mgr.get_unlabeled_counter_latest('osd', f'osd.{osd_id}', 'osd.numpg')
mgr.get_unlabeled_counter_latest('osd', f'osd.{osd_id}', 'osd.stat_bytes')
mgr.get_unlabeled_counter_latest('osd', f'osd.{osd_id}', 'osd.stat_bytes_used')
```

**Workaround — two paths depending on configuration:**

- **`ceph-exporter` daemon** (`:9926/metrics`, stable from v18.2.0) — a per-host C++ daemon that reads each OSD's admin socket directly and exports all perf counters including `osd_numpg`, `osd_stat_bytes`. Does not go through MGR RAM at all. Source: [`src/exporter/`](https://github.com/ceph/ceph/tree/v20.3.0/src/exporter). In Rook, runs as a Kubernetes DaemonSet named `rook-ceph-exporter` ([`rook/module.py:485`](https://github.com/ceph/ceph/blob/v20.3.0/src/pybind/mgr/rook/module.py#L485)).
- **Prometheus MGR module** (`:9283/metrics`) — calls [`get_perf_counters()`](https://github.com/ceph/ceph/blob/v20.3.0/src/pybind/mgr/prometheus/module.py#L1641) which reads the same MGR RAM circular buffers as the dashboard. **Disabled by default**: [`exclude_perf_counters=True`](https://github.com/ceph/ceph/blob/v20.3.0/src/pybind/mgr/prometheus/module.py#L590) — must be explicitly set to `false`. Degrades MGR performance in large clusters.


## Other affected endpoints

| Dashboard endpoint | MGR-internal call | Source | Workaround |
|---|---|---|---|
| `GET /api/cephfs/{id}` | `mgr.get_unlabeled_counter_latest('mds', 'mds.N', ...)` — four counters: `mds_mem.ino`, `mds_mem.dn`, `mds_mem.dir`, `mds_mem.cap` | [cephfs.py:249](https://github.com/ceph/ceph/blob/v20.3.0/src/pybind/mgr/dashboard/controllers/cephfs.py#L249) | Prometheus|
| `GET /api/monitor` | `mgr.get_unlabeled_counter('mon', 'mon.N', 'mon.num_sessions')` (full history, not just latest) | [monitor.py:119](https://github.com/ceph/ceph/blob/v20.3.0/src/pybind/mgr/dashboard/controllers/monitor.py#L119) | Prometheus|
| `GET /api/host` | `mgr.list_servers()` → `_ceph_get_server(None)` — **only when cephadm is not installed**; with cephadm, `orch.hosts.list()` is used instead (reachable via `mgr_command`) | [host.py:171](https://github.com/ceph/ceph/blob/v20.3.0/src/pybind/mgr/dashboard/controllers/host.py#L171) | See section below |

---

## `GET /api/host` — live daemon registry

**Important:** `mgr.list_servers()` is only the fallback path when no orchestrator is installed. Both cephadm and Rook implement `orch.hosts.list()`, so `get_hosts()` never reaches `mgr.list_servers()` in either standard deployment. Both are reachable from a container via `mgr_command({"prefix":"orch host ls"})`. The problem below only applies to bare clusters with no orchestrator.

The data source differs by orchestrator:
- **cephadm**: queries its own host inventory — bare-metal hostnames, labels, addresses, maintenance status.
- **Rook**: queries the **Kubernetes node API** ([`rook_cluster.py:892`](https://github.com/ceph/ceph/blob/v20.3.0/src/pybind/mgr/rook/rook_cluster.py#L892) — `self.nodes.items`). Returns K8s node names and addresses from `node.status.addresses`. Labels are only the ones prefixed `ceph-label/`.

`list_servers()` calls into a C++ map inside the MGR process built from daemon heartbeats. Every daemon (OSD, MDS, MON, RGW, MGR itself) sends its hostname when it first connects to the active MGR. The C++ layer aggregates this into a per-host structure:

[`src/pybind/mgr/mgr_module.py:1732`](https://github.com/ceph/ceph/blob/v20.3.0/src/pybind/mgr/mgr_module.py#L1732)

### What's lost

| Data field | list_servers() | mon_command workaround |
|---|---|---|
| OSD hostnames | ✓ | ✓ `osd metadata` |
| MDS / MON hostnames | ✓ | ✓ `mds metadata` / `mon metadata` |
| RGW daemon hostnames | ✓ | ✗ no `rgw metadata` mon_command |
| MGR daemon hostnames | ✓ | ✗ `mgr dump` has no hostname field |
| "Currently reporting" signal | ✓ only live daemons appear | ✗ osd dump shows all OSDs including down |
| Ceph version per host | ✓ per-host version string | ✗ not in metadata commands |

The practical impact: on clusters with cephadm or Rook (both standard), this is a non-issue — use `mgr_command("orch host ls")`. On bare clusters with no orchestrator, the mon_command workaround covers OSD/MDS/MON hosts but silently omits hosts that only run RGW or MGR daemons.

---

> Everything else in the dashboard — health, pool, OSD ops, config, CephFS, RBD, RGW, NFS, SMB, orchestration — is reachable from a container via `mon_command`, `mgr_command`, go-ceph libraries, or HTTP. Some of the data that cannot leave the MGR process is the perf counter circular buffers and the live daemon registry above.
