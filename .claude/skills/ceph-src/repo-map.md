# repo-map.md — navigating `third_party/ceph/`

All paths relative to `third_party/ceph/`.

Top-level dirs are stable across releases. File-level paths inside them
DO churn — `grep`/`find`/`git log` before quoting them, don't trust
training-data filenames.

## High-level layout

- `src/` — all C++/Python source.
- `doc/` — Sphinx/RST user + developer docs. Authoritative for
  supported behavior and options.
- `qa/` — integration test suites (`qa/suites/`, `qa/tasks/`,
  `qa/workunits/`).
- `cmake/`, `CMakeLists.txt`, `do_cmake.sh`, `install-deps.sh`,
  `debian/`, `ceph.spec.in` — build/packaging.
- `systemd/`, `selinux/`, `etc/`, `examples/`, `monitoring/`,
  `container/`, `admin/` — deployment-side assets.
- `PendingReleaseNotes` — staged notes for the next release.
- Root anchors: `README.md`, `CONTRIBUTING.rst`, `SubmittingPatches.rst`,
  `SECURITY.md`, `CodingStyle`, `AUTHORS`, `COPYING*`.

## `src/` by component

- **RADOS core:** `osd/`, `mon/`, `mgr/`, `mds/`, `messages/`, `msg/`
  (messenger v1/v2), `osdc/` (client), `crush/`, `auth/` (cephx),
  `crypto/`, `common/`, `global/`.
- **Storage backends:** `os/` (BlueStore, FileStore), `kv/` (RocksDB),
  `blk/`, `journal/`, `erasure-code/` (jerasure, lrc, shec),
  `compressor/`, `cls/` + `objclass/` (object classes).
- **Object (RGW):** `rgw/` — S3/Swift, multisite, zones, lifecycle,
  IAM, bucket sharding. Admin CLI: `src/rgw/radosgw-admin/`.
- **Block (RBD):** `librbd/`, `rbd_fuse/`, `rbd_replay/`.
- **File (CephFS):** server in `mds/`; client (libcephfs / ceph-fuse)
  in `client/`. Scattered support: `tools/cephfs/`,
  `tools/cephfs_mirror/`, `cls/cephfs/`, `include/cephfs/`,
  `pybind/cephfs/`. No top-level `src/cephfs/`.
- **Orchestration / provisioning:** `cephadm/` (Python container
  orchestrator), `ceph-volume/` (OSD provisioning, LVM, dm-crypt).
- **Bindings & APIs:** `pybind/` (rados, rbd, cephfs, mgr, rgw),
  `include/` (public C/C++ headers).
- **Tools / standalone binaries:** `tools/` (crushtool, monmaptool,
  osdmaptool, ceph_objectstore_tool, ceph_dencoder, ceph_authtool,
  ceph_kvstore_tool, …).
- **Experimental / specialized:** `crimson/` (Seastar-based next-gen
  OSD), `neorados/` (next-gen RADOS client), `nvmeof/` (NVMe-oF).
- **Tests:** `src/test/` (unit), separate from `qa/` (integration).

## `doc/` by topic

- `start/`, `install/` — first-cluster + platform install.
- `cephadm/` — orchestration deployment.
- `rados/` — RADOS, pools/PGs/EC, CRUSH operations, configuration
  reference.
- `radosgw/` — RGW S3/Swift/multisite. (Older releases used
  `doc/rgw/`; this checkout has `doc/radosgw/` — verify on your
  checkout.)
- `rbd/` — block device, mirroring, snapshots, clones.
- `cephfs/` — filesystem, MDS, snapshots, quotas, multi-fs.
- `mgr/` — manager modules (dashboard, prometheus, balancer,
  pg_autoscaler, orchestrator, …).
- `ceph-volume/` — OSD provisioning.
- `dev/` — internals, protocols, peering, encoding, developer guide.
- `security/` — cephx, encryption, access control.
- `api/` — librados / librbd / libcephfs API reference.
- `releases/` — one `.rst` per named release (squid, reef, quincy, …)
  + `releases.yml`.
- `changelog/` — per-version changelog entries.
- `man/8/` — man page sources for CLIs and daemons.
- `architecture.rst`, `glossary.rst` — system overview + terminology.

## Topic → start here

| Question topic | Docs starting point | Code starting point |
|---|---|---|
| CRUSH, placement, weight, failure domains | `doc/rados/operations/crush*` | `src/crush/` |
| Pools, PGs, replication, erasure coding | `doc/rados/configuration/`, `doc/rados/operations/` | `src/osd/`, `src/erasure-code/` |
| RADOS protocol, OSD ops, peering, recovery | `doc/dev/` (peering/protocol/encoding) | `src/messages/`, `src/osd/`, `src/osdc/` |
| RGW (S3/Swift, multisite, zones, lifecycle, IAM, STS) | `doc/radosgw/` | `src/rgw/` |
| RBD mirroring, snapshots, clones | `doc/rbd/` | `src/librbd/` |
| CephFS, MDS, snapshots, quotas | `doc/cephfs/`, `doc/dev/mds_internals/` | `src/mds/`, `src/client/` |
| Manager modules (dashboard, prometheus, balancer, pg_autoscaler) | `doc/mgr/` | `src/mgr/` (base) + `src/pybind/mgr/<module>/` |
| Authentication (cephx) | `doc/dev/cephx*.rst`, `doc/security/` | `src/auth/`, `src/crypto/` |
| Monitor, Paxos, quorum, elections | `doc/dev/mon-elections.rst`, `doc/dev/mon-on-disk-formats.rst` | `src/mon/` |
| cephadm orchestration | `doc/cephadm/` | `src/cephadm/` (+ `cephadmlib/`) |
| ceph-volume, OSD provisioning, LVM, dm-crypt | `doc/ceph-volume/` | `src/ceph-volume/` |
| Configuration options (canonical list, defaults, types) | `doc/rados/configuration/`, `doc/dev/config.rst` | `src/common/options/` (`*.yaml.in` per daemon) |
| `ceph config get/set/dump`, monitor config store, runtime overrides | `doc/rados/configuration/ceph-conf.rst` (and siblings) | `src/mon/ConfigMonitor*`, `src/common/config*` |
| Release notes, version history | `doc/releases/<release>.rst`, `doc/releases/releases.yml` | `PendingReleaseNotes` (staged for next release) |
| BlueStore, object store internals | `doc/dev/`, `doc/rados/configuration/bluestore-config-ref.rst` | `src/os/bluestore/` |
| Messenger v1/v2 wire protocol | `doc/dev/` (msgr docs) | `src/msg/`, `src/messages/` |

If a topic isn't here, run
`find third_party/ceph/doc -type d -name '*<keyword>*'` plus a
`grep -r` in `third_party/ceph/src/` before guessing.

## Ground-truth checks before answering

- For "what does config option X do / what is its default": read
  `src/common/options/*.yaml.in` (canonical) AND the matching prose in
  `doc/rados/configuration/`. They must agree.
- **When docs and code disagree on behavior, code wins; flag the
  discrepancy in the answer.**
- For "what does CLI command Y do": consult `doc/man/8/<command>.rst`
  first; for behavior, follow the source from the corresponding
  `src/tools/` or daemon dir.
- For "when did feature Z land / change": check
  `doc/releases/<release>.rst` and `PendingReleaseNotes`; corroborate
  with `git -C third_party/ceph log -- <path>` once you have a path.
- For S3 / Swift API behavior in RGW: `doc/radosgw/` is authoritative
  for what's supported; `src/rgw/` for actual handling. Don't
  extrapolate from AWS S3 docs — RGW deviates.
- For "what command does daemon X run": grep under the daemon's
  `src/<daemon>/` dir; do not infer from blog posts.

## What NOT to assume

- Filenames inside `src/` and `doc/` move between releases — grep
  before quoting them.
- Behavior described in old blog posts or your training data is
  frequently stale; the cloned tree is the current truth.
- `doc/` can lag `src/` on edge behavior — see the "code wins" rule
  above.
- This is read-only reference. If a task requires building/modifying
  the Ceph C++ source, that is out of scope — say so.
