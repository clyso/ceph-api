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

