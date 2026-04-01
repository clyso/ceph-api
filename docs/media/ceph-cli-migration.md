# ceph CLI: Challenges in migration 

The `ceph` CLI ([`src/ceph.in`](https://github.com/ceph/ceph/blob/v20.3.0/src/ceph.in)) dispatches commands via three paths. Two work over the network from any container with a keyring and monitor IPs. One requires a Unix socket on the local filesystem.

---

## Dispatch paths

| Path | Command form | Portable from container |
|---|---|---|
| `mon_command` | `ceph osd ls`, `ceph pg dump`, `ceph df`, `ceph auth ls`, … | ✓ |
| `mgr_command` | `ceph fs ls`, `ceph orch host ls`, `ceph dashboard …`, … | ✓ |
| admin socket | `ceph daemon <id> <cmd>` | ✗ requires socket mount |

---

## Example: `ceph daemon osd.0 perf dump`

`ceph daemon` always goes to the admin socket at `/var/run/ceph/$cluster-$name.asok`. The socket is an AF_UNIX stream socket on the host filesystem — it does not exist as a network endpoint.

```
ceph-api container                    OSD host
┌─────────────────────┐               ┌──────────────────────────────┐
│                     │   network     │  ┌─────────┐                 │
│  ceph osd ls        │──────────────▶│  │   MON   │  mon_command ✓  │
│  ceph pg dump       │               │  └─────────┘                 │
│                     │               │                              │
│  ceph daemon osd.0  │               │  /var/run/ceph/              │
│    perf dump        │──── ✗ ───────▶│    ceph-osd.0.asok           │
│                     │  no network   │  (AF_UNIX, host-local only)  │
└─────────────────────┘               └──────────────────────────────┘
```

**Workaround:** Mount the host socket directory as a volume:
```yaml
volumes:
  - /var/run/ceph:/var/run/ceph:ro
```
Then `ceph daemon osd.0 perf dump` works from the container. In Kubernetes/Rook the ceph-exporter DaemonSet already does this, so ceph-api should read perf counters from `:9926/metrics` rather than opening sockets directly.


## `ceph tell`

`ceph tell <type>.<id> <cmd>` sends a command to a specific daemon over the network via the MON — no socket mount required. All four daemon types (OSD, MDS, MON, MGR) forward incoming `ceph tell` commands directly into their admin socket queue ([`OSD.cc:7443`](https://github.com/ceph/ceph/blob/v20.3.0/src/osd/OSD.cc#L7443), [`MDSDaemon.cc:779`](https://github.com/ceph/ceph/blob/v20.3.0/src/mds/MDSDaemon.cc#L779), [`Monitor.cc:3456`](https://github.com/ceph/ceph/blob/v20.3.0/src/mon/Monitor.cc#L3456), [`DaemonServer.cc:1652`](https://github.com/ceph/ceph/blob/v20.3.0/src/mgr/DaemonServer.cc#L1652)):

```cpp
cct->get_admin_socket()->queue_tell_command(m);
```

This means every command registered on a daemon's admin socket is also reachable via `ceph tell`.
### Commands with no `ceph tell` alternative

**`ceph daemonperf`** has no network equivalent. It is a CLI-level subcommand ([`src/ceph.in:837`](https://github.com/ceph/ceph/blob/v20.3.0/src/ceph.in#L837)) that streams live perf stats by polling `perf schema` + `perf dump` on the socket in a loop. `daemonperf` is not an admin socket command itself — passing it to the socket returns an error. It must be invoked with a direct socket path:

```bash
# Name resolution is broken for daemonperf — use the full path instead:
sudo ceph daemonperf /var/run/ceph/<fsid>/ceph-osd.0.asok
```