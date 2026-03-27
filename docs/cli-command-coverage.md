# CLI Command Coverage Reference

Source-verified coverage map for every Ceph CLI tool command, based on:
- `src/mon/MonCommands.h` — every `COMMAND(...)` registration
- `src/mgr/MgrCommands.h` — MGR-side C++ commands
- `src/pybind/mgr/orchestrator/module.py` — all `orch *` commands
- `src/pybind/mgr/cephadm/module.py` — `cephadm *` and `orch client-keyring *` commands
- `src/pybind/mgr/volumes/module.py`, `balancer/`, `snap_schedule/`, `telemetry/`, `alerts/`
- `src/tools/rbd/action/*.cc` — every `rbd` subcommand
- `src/include/rbd/librbd.h` — C API for go-ceph coverage checking
- `src/rgw/radosgw-admin/radosgw-admin.cc` — all RGW admin CLI commands
- `src/rgw/driver/rados/rgw_sal_rados.cc` — Admin HTTP API route registrations
- `src/tools/rados/rados.cc` — all `rados` CLI subcommands
- `src/common/admin_socket.cc`, `src/osd/OSD.cc`, `src/mon/Monitor.cc` — admin socket registrations
- `src/cephadm/cephadm.py` — direct cephadm binary subcommands
- `src/tools/cephfs/*.cc` — offline CephFS recovery tools

## Legend

| Symbol | Meaning |
|--------|---------|
| ✅ FEASIBLE | Implementable via ExecMon/ExecMgr/go-ceph. Just needs a gRPC endpoint. |
| ⚠ CONDITIONAL | Implementable but requires extra work: binary streaming, extra library, or specific deployment conditions. |
| 🔒 CONDITIONAL (backend) | Requires cephadm or rook orchestrator backend configured. |
| ❌ INFEASIBLE | Cannot be implemented remotely. Requires host-local access, Unix socket, or kernel module. |

## Routing Architecture

| Call path | When to use | go-ceph function |
|-----------|-------------|-----------------|
| **ExecMon** | Any `ceph <cmd>` that goes to a Monitor | `conn.MonCommand([]byte(cmd))` |
| **ExecMon + inbuf** | Commands that require binary/text input (`auth import`, `osd setcrushmap`) | `conn.MonCommandWithInputBuffer(cmd, data)` |
| **ExecMgr** | Any mgr module command (`balancer`, `orch`, `fs volume`, `cephadm`, etc.) | `conn.MgrCommand([][]byte{cmd})` |
| **go-ceph/rbd** | All `rbd` CLI equivalents | `rbd.CreateImage`, `image.Snapshot`, etc. |
| **go-ceph/rados IoCtx** | `rados put/get/stat/omap/*` | `ioctx.Read`, `ioctx.Write`, etc. |
| **go-ceph/rgw/admin** | `radosgw-admin user/bucket/quota/usage/caps/keys` | `admin.CreateUser`, etc. |
| **go-ceph/cephfs/admin** | `ceph fs volume/subvolume` | `FSAdmin.CreateSubVolume`, etc. |
| **aws-sdk-go-v2 s3** | RGW S3-protocol ops (lifecycle, CORS, notifications, SSE, replication) | `s3.PutBucketLifecycleConfiguration` |
| **❌ Admin socket** | `ceph daemon <id> <cmd>`, `ceph tell` | Unix socket — no remote path |

---

## 1. `ceph` CLI — mon_command / mgr_command

### 1.1 Cluster Status & Info

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph status` | MonCommand | ✅ | Already implemented: `StatusService.GetCephStatus` |
| `ceph health [detail]` | MonCommand | ✅ | |
| `ceph health mute <code>` | MonCommand | ✅ | Mutes a specific health alert |
| `ceph health unmute [code]` | MonCommand | ✅ | |
| `ceph df [detail]` | MonCommand | ✅ | Cluster free space per pool |
| `ceph fsid` | MonCommand | ✅ | Returns cluster UUID |
| `ceph report` | MonCommand | ✅ | Already implemented: `StatusService.GetCephReport` |
| `ceph features` | MonCommand | ✅ | Connected daemon feature bits |
| `ceph quorum_status` | MonCommand | ✅ | |
| `ceph time-sync-status` | MonCommand | ✅ | NTP/chrony per-mon |
| `ceph node ls [type]` | MonCommand | ✅ | All nodes by type |
| `ceph versions` | MonCommand | ✅ | All running daemon versions |
| `ceph log <text...>` | MonCommand | ✅ | Append to cluster audit log |
| `ceph log last [n] [level] [channel]` | MonCommand | ✅ | Log tail (polling workaround for streaming) |
| `ceph tell <daemon> <cmd>` | Admin socket | ❌ | Routed to daemon's Unix socket; no remote path |
| `ceph -w` / `--watch` | monitor_log2() | ⚠ | Push stream; go-ceph gap. Workaround: poll `log last` every 3–5 s filtering by Paxos version |
| `ceph ping <mon>` | ping_monitor() | ❌ | Uses a different protocol helper, not mon_command |

### 1.2 Monitor Management

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph mon stat` | MonCommand | ✅ | |
| `ceph mon dump [epoch]` | MonCommand | ✅ | Already implemented: `StatusService.GetCephMonDump` |
| `ceph mon getmap [epoch]` | MonCommand | ⚠ | Returns **binary encoded monmap** in outbuf (not JSON); treat response as raw bytes or use `mon dump` instead |
| `ceph mon add <name> <addr>` | MonCommand | ✅ | |
| `ceph mon rm <name>` | MonCommand | ✅ | |
| `ceph mon feature ls` | MonCommand | ✅ | |
| `ceph mon feature set <name>` | MonCommand | ✅ | |
| `ceph mon set-rank <name> <rank>` | MonCommand | ✅ | |
| `ceph mon set-addrs <name> <addrs>` | MonCommand | ✅ | |
| `ceph mon set-weight <name> <weight>` | MonCommand | ✅ | |
| `ceph mon enable-msgr2` | MonCommand | ✅ | |
| `ceph mon set election_strategy <s>` | MonCommand | ✅ | classic / disallow / connectivity |
| `ceph mon enable_stretch_mode` | MonCommand | ✅ | Stretch topology |
| `ceph mon disable_stretch_mode` | MonCommand | ✅ | |
| `ceph mon ok-to-stop <ids>` | MonCommand | ✅ | Safety check |
| `ceph mon ok-to-rm <id>` | MonCommand | ✅ | |
| `ceph mon metadata [id]` | MonCommand | ✅ | |
| `ceph mon scrub` | MonCommand | ✅ | Triggers mon store scrub |

### 1.3 Auth / CephX (✅ Already in ceph-api: `ClusterService`)

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph auth ls` | MonCommand | ✅ | `ClusterService.GetUsers` |
| `ceph auth export [entity]` | MonCommand | ✅ | `ClusterService.ExportUser` — default output is keyring text; use `format=json` |
| `ceph auth get <entity>` | MonCommand | ✅ | |
| `ceph auth get-key <entity>` | MonCommand | ✅ | Returns just the base64 key |
| `ceph auth add <entity> [caps]` | MonCommand | ✅ | `ClusterService.CreateUser` |
| `ceph auth import` | MonCommand + **inbuf** | ✅ | `ClusterService.CreateUser` (import path); inbuf = keyring text |
| `ceph auth get-or-create <entity>` | MonCommand | ✅ | Idempotent create |
| `ceph auth get-or-create-key <entity>` | MonCommand | ✅ | Returns just the key |
| `ceph auth get-or-create-pending <entity>` | MonCommand | ✅ | Key rotation staging |
| `ceph auth clear-pending <entity>` | MonCommand | ✅ | |
| `ceph auth commit-pending <entity>` | MonCommand | ✅ | Rotates pending key into active |
| `ceph auth rotate <entity>` | MonCommand | ✅ | |
| `ceph auth caps <entity> <caps>` | MonCommand | ✅ | `ClusterService.UpdateUser` |
| `ceph auth rm <entity>` | MonCommand | ✅ | `ClusterService.DeleteUser` |
| `ceph fs authorize <fs> <entity> <caps>` | MonCommand | ✅ | CephFS-scoped auth |

### 1.4 OSD Map & Metadata

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph osd stat` | MonCommand | ✅ | |
| `ceph osd dump [epoch]` | MonCommand | ✅ | Already implemented: `StatusService.GetCephOsdDump` |
| `ceph osd info [id]` | MonCommand | ✅ | Single OSD info |
| `ceph osd tree [epoch] [states]` | MonCommand | ✅ | CRUSH tree view |
| `ceph osd tree-from <bucket>` | MonCommand | ✅ | CRUSH tree from bucket |
| `ceph osd ls [epoch]` | MonCommand | ✅ | List all OSD IDs |
| `ceph osd getmap [epoch]` | MonCommand | ⚠ | Returns **binary encoded OSD map** in outbuf; use `osd dump` for JSON |
| `ceph osd getmaxosd` | MonCommand | ✅ | |
| `ceph osd ls-tree <bucket>` | MonCommand | ✅ | OSDs under a CRUSH bucket |
| `ceph osd find <id>` | MonCommand | ✅ | CRUSH location |
| `ceph osd metadata [id]` | MonCommand | ✅ | kernel/distro/device metadata |
| `ceph osd count-metadata <prop>` | MonCommand | ✅ | |
| `ceph osd versions` | MonCommand | ✅ | |
| `ceph osd numa-status` | MonCommand | ✅ | |
| `ceph osd map <pool> <object>` | MonCommand | ✅ | Find PG for an object |
| `ceph osd getcrushmap [epoch]` | MonCommand | ⚠ | Returns **binary encoded CRUSH map**; use `osd crush dump` for JSON |
| `ceph osd utilization` | MonCommand | ✅ | PG distribution stats |

### 1.5 OSD State Management

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph osd down <ids>` | MonCommand | ✅ | |
| `ceph osd stop <ids>` | MonCommand | ✅ | |
| `ceph osd out <ids>` | MonCommand | ✅ | |
| `ceph osd in <ids>` | MonCommand | ✅ | |
| `ceph osd pause` | MonCommand | ✅ | Pauses all OSD I/O |
| `ceph osd unpause` | MonCommand | ✅ | |
| `ceph osd reweight <id> <weight>` | MonCommand | ✅ | Async — triggers PG remapping; return immediately, poll status |
| `ceph osd reweightn <weights>` | MonCommand | ✅ | Batch reweight |
| `ceph osd set-group <flags> <who>` | MonCommand | ✅ | Per-OSD flags (noup/nodown/noin/noout) |
| `ceph osd unset-group <flags> <who>` | MonCommand | ✅ | |
| `ceph osd set <key>` | MonCommand | ✅ | Cluster-wide OSD flag (full/pause/noup/nodown/...) |
| `ceph osd unset <key>` | MonCommand | ✅ | |
| `ceph osd require-osd-release <release>` | MonCommand | ✅ | |
| `ceph osd get-require-min-compat-client` | MonCommand | ✅ | |
| `ceph osd set-require-min-compat-client <ver>` | MonCommand | ✅ | |
| `ceph osd primary-affinity <id> <w>` | MonCommand | ✅ | |
| `ceph osd safe-to-destroy <ids>` | MonCommand | ✅ | Safety check before destroy |
| `ceph osd ok-to-stop <ids>` | MonCommand | ✅ | Safety check before stop |
| `ceph osd destroy <id>` | MonCommand | ✅ | Marks OSD destroyed (keys wiped) |
| `ceph osd purge <id>` | MonCommand | ✅ | Removes all OSD traces |
| `ceph osd purge-new <id>` | MonCommand | ✅ | Purges partially-created OSD |
| `ceph osd lost <id>` | MonCommand | ✅ | ⚠ DATA LOSS — marks OSD permanently lost |
| `ceph osd new <uuid>` | MonCommand + optional inbuf | ✅ | inbuf optional JSON with CephX keys |
| `ceph osd setmaxosd <n>` | MonCommand | ✅ | |
| `ceph osd set-full-ratio <r>` | MonCommand | ✅ | |
| `ceph osd set-backfillfull-ratio <r>` | MonCommand | ✅ | |
| `ceph osd set-nearfull-ratio <r>` | MonCommand | ✅ | |
| `ceph osd perf` | MonCommand | ⚠ | Returns **current snapshot** of commit/apply latency only. NOT the 20-point rolling window (that's in MGR heap). See infeasible-operations.md §Perf Counters. |
| `ceph osd scrub <id>` | MonCommand | ✅ | Fire-and-forget; 0 = accepted, not completed |
| `ceph osd deep-scrub <id>` | MonCommand | ✅ | Same |
| `ceph osd repair <id>` | MonCommand | ✅ | Same |
| `ceph osd nodown <ids>` | MonCommand | ✅ | Per-OSD nodown flag |
| `ceph osd noup <ids>` | MonCommand | ✅ | |
| `ceph osd noin <ids>` | MonCommand | ✅ | |
| `ceph osd noout <ids>` | MonCommand | ✅ | |
| `ceph osd rm <ids>` | MonCommand | ✅ | Remove OSD from map |
| `ceph osd add-nodown <ids>` | MonCommand | ✅ | |
| `ceph osd rm-nodown <ids>` | MonCommand | ✅ | |
| `ceph osd blocklist add <addr> [expire]` | MonCommand | ✅ | Adds blocklist entry |
| `ceph osd blocklist rm <addr>` | MonCommand | ✅ | |
| `ceph osd blocklist ls` | MonCommand | ✅ | |
| `ceph osd blocklist clear` | MonCommand | ✅ | |
| `ceph osd primary-temp <pgid> <id>` | MonCommand | ✅ | Override primary for a PG |
| `ceph osd pg-temp <pgid> <ids>` | MonCommand | ✅ | Override acting set for a PG |
| `ceph osd force-create-pg <pgid>` | MonCommand | ✅ | |
| `ceph osd reweight-by-utilization` | MgrCommand | ✅ | Auto-reweight by OSD utilization |
| `ceph osd reweight-by-pg` | MgrCommand | ✅ | Auto-reweight by PG count |
| `ceph osd test-reweight-by-utilization` | MgrCommand | ✅ | Dry-run of above |
| `ceph osd df [tree]` | MgrCommand | ✅ | OSD utilization table; use ExecMgr |

### 1.6 Pool Management

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph osd pool create <name> [pg_num]` | MonCommand | ✅ | ⚠ Eventual consistency — pool in OSDMap immediately, OSDs receive update async (~100–500 ms) |
| `ceph osd pool delete <name>` | MonCommand | ✅ | Requires `--yes-i-really-really-mean-it` and `mon_allow_pool_delete=true` |
| `ceph osd pool ls [detail]` | MonCommand | ✅ | |
| `ceph osd pool rename <old> <new>` | MonCommand | ✅ | |
| `ceph osd pool set <pool> <key> <val>` | MonCommand | ✅ | Many keys: size, min_size, pg_num, pgp_num, crush_rule, etc. |
| `ceph osd pool get <pool> <key>` | MonCommand | ✅ | |
| `ceph osd pool get-quota <pool>` | MonCommand | ✅ | |
| `ceph osd pool set-quota <pool> <type> <val>` | MonCommand | ✅ | max_objects or max_bytes |
| `ceph osd pool stats [pool]` | MonCommand | ✅ | |
| `ceph osd pool mksnap <pool> <snap>` | MonCommand | ✅ | Pool-level snapshot |
| `ceph osd pool rmsnap <pool> <snap>` | MonCommand | ✅ | |
| `ceph osd pool application enable <pool> <app>` | MonCommand | ✅ | Tag pool for rbd/cephfs/rgw |
| `ceph osd pool application disable <pool> <app>` | MonCommand | ✅ | |
| `ceph osd pool application get <pool>` | MonCommand | ✅ | |
| `ceph osd pool autoscale-status` | MgrCommand | ✅ | PG autoscaler status |

### 1.7 PG Operations

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph pg dump` | MgrCommand | ✅ | Already implemented: `StatusService.GetCephPgDump` |
| `ceph pg stat` | MgrCommand | ✅ | |
| `ceph pg dump_pools_json` | MgrCommand | ✅ | Per-pool PG stats |
| `ceph pg dump_stuck [inactive\|unclean\|stale\|undersized\|degraded]` | MonCommand | ✅ | |
| `ceph pg map <pgid>` | MonCommand | ✅ | OSD mapping for a PG |
| `ceph pg ls-by-pool <pool>` | MonCommand | ✅ | |
| `ceph pg ls-by-osd <id>` | MonCommand | ✅ | |
| `ceph pg ls-by-primary <id>` | MonCommand | ✅ | |
| `ceph pg scrub <pgid>` | MonCommand | ✅ | Fire-and-forget |
| `ceph pg deep-scrub <pgid>` | MonCommand | ✅ | |
| `ceph pg repair <pgid>` | MonCommand | ✅ | |
| `ceph pg force-recovery <pgids>` | MonCommand | ✅ | |
| `ceph pg force-backfill <pgids>` | MonCommand | ✅ | |
| `ceph pg cancel-force-recovery <pgids>` | MonCommand | ✅ | |
| `ceph pg cancel-force-backfill <pgids>` | MonCommand | ✅ | |
| `ceph pg getmap` | MonCommand | ⚠ | Returns **binary PG map** in outbuf |
| `ceph pg query <pgid>` | MonCommand | ✅ | Detailed PG state |
| `ceph pg debug <keyword>` | MonCommand | ✅ | unfound_objects_exist / degraded_pgs_exist |

### 1.8 CRUSH (✅ Partially in ceph-api: `CrushRuleService`)

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph osd crush dump` | MonCommand | ✅ | Full CRUSH map JSON; `CrushRuleService.ListRules` |
| `ceph osd crush show-tunables` | MonCommand | ✅ | |
| `ceph osd crush set-all-straw-buckets-to-straw2` | MonCommand | ✅ | |
| `ceph osd crush tunables <profile>` | MonCommand | ✅ | |
| `ceph osd crush add-bucket <name> <type>` | MonCommand | ✅ | |
| `ceph osd crush rename-bucket <old> <new>` | MonCommand | ✅ | |
| `ceph osd crush move <name> <args>` | MonCommand | ✅ | |
| `ceph osd crush remove <name>` | MonCommand | ✅ | |
| `ceph osd crush reweight <name> <w>` | MonCommand | ✅ | |
| `ceph osd crush reweight-all` | MonCommand | ✅ | |
| `ceph osd crush reweight-subtree <name> <w>` | MonCommand | ✅ | |
| `ceph osd crush link <name> <args>` | MonCommand | ✅ | |
| `ceph osd crush unlink <name>` | MonCommand | ✅ | |
| `ceph osd crush add <id> <w> <args>` | MonCommand | ✅ | |
| `ceph osd crush set <id> <w> <args>` | MonCommand + optional inbuf | ⚠ | inbuf = full CRUSH map binary when called without OSD args |
| `ceph osd crush rm <name>` | MonCommand | ✅ | |
| `ceph osd crush class ls` | MonCommand | ✅ | |
| `ceph osd crush class create <name>` | MonCommand | ✅ | |
| `ceph osd crush class rm <name>` | MonCommand | ✅ | |
| `ceph osd crush class rename <old> <new>` | MonCommand | ✅ | |
| `ceph osd crush weight-set ls` | MonCommand | ✅ | |
| `ceph osd crush weight-set dump` | MonCommand | ✅ | |
| `ceph osd crush weight-set create <pool> <mode>` | MonCommand | ✅ | |
| `ceph osd crush weight-set modify <pool> <item> <w>` | MonCommand | ✅ | |
| `ceph osd crush weight-set rm <pool>` | MonCommand | ✅ | |
| `ceph osd crush rule create-replicated` | MonCommand | ✅ | `CrushRuleService.CreateRule` |
| `ceph osd crush rule create-erasure` | MonCommand | ✅ | `CrushRuleService.CreateRule` |
| `ceph osd crush rule create-simple` | MonCommand | ✅ | |
| `ceph osd crush rule ls` | MonCommand | ✅ | |
| `ceph osd crush rule rm <name>` | MonCommand | ✅ | `CrushRuleService.DeleteRule` |
| `ceph osd crush tree` | MonCommand | ✅ | |
| `ceph osd crush ls <node>` | MonCommand | ✅ | |
| `ceph osd crush get-device-class <id>` | MonCommand | ✅ | |
| `ceph osd crush set-device-class <class> <ids>` | MonCommand | ✅ | |
| `ceph osd crush rm-device-class <ids>` | MonCommand | ✅ | |
| `ceph osd setcrushmap` | MonCommand + **inbuf required** | ⚠ | inbuf = binary CRUSH map; `ExecMonWithInputBuff` |

### 1.9 Erasure-Code Profiles

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph osd erasure-code-profile ls` | MonCommand | ✅ | |
| `ceph osd erasure-code-profile get <name>` | MonCommand | ✅ | |
| `ceph osd erasure-code-profile set <name> <props>` | MonCommand | ✅ | |
| `ceph osd erasure-code-profile rm <name>` | MonCommand | ✅ | |

### 1.10 Config Management

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph config get <who> [key]` | MonCommand | ✅ | |
| `ceph config set <who> <key> <val>` | MonCommand | ✅ | |
| `ceph config rm <who> <key>` | MonCommand | ✅ | |
| `ceph config dump` | MonCommand | ✅ | All non-default config values |
| `ceph config diff` | MonCommand | ✅ | Diff from defaults |
| `ceph config log` | MonCommand | ✅ | Config change audit |
| `ceph config reset <ver>` | MonCommand | ✅ | Reset config to a prior version |
| `ceph config assimilate-conf` | MonCommand + **inbuf** | ⚠ | inbuf = ceph.conf text; `ExecMonWithInputBuff` |
| `ceph config show <who>` | MonCommand | ✅ | Runtime config (Monitor view — may differ from live daemon) |
| `ceph config show-with-defaults <who>` | MonCommand | ✅ | |
| `ceph config-key get <key>` | MonCommand | ✅ | Requires `mon 'allow *'` |
| `ceph config-key set <key> <val>` | MonCommand | ✅ | Requires admin cap |
| `ceph config-key rm <key>` | MonCommand | ✅ | |
| `ceph config-key ls` | MonCommand | ✅ | |
| `ceph config-key exists <key>` | MonCommand | ✅ | |
| `ceph config-key dump` | MonCommand | ✅ | |

### 1.11 MDS / CephFS (Mon-level)

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph mds stat` | MonCommand | ✅ | |
| `ceph mds dump [epoch]` | MonCommand | ✅ | |
| `ceph mds fail <id>` | MonCommand | ✅ | Marks MDS as failed |
| `ceph mds repaired <pgid>` | MonCommand | ✅ | |
| `ceph mds compat show` | MonCommand | ✅ | |
| `ceph mds compat rm_compat <f>` | MonCommand | ✅ | |
| `ceph mds compat rm_incompat <f>` | MonCommand | ✅ | |
| `ceph mds ok-to-stop <ids>` | MonCommand | ✅ | Safety check |
| `ceph fs new <name> <meta_pool> <data_pool>` | MonCommand | ✅ | |
| `ceph fs rm <name>` | MonCommand | ✅ | |
| `ceph fs ls` | MonCommand | ✅ | |
| `ceph fs get <name>` | MonCommand | ✅ | |
| `ceph fs set <name> <key> <val>` | MonCommand | ✅ | |
| `ceph fs add_data_pool <name> <pool>` | MonCommand | ✅ | |
| `ceph fs rm_data_pool <name> <pool>` | MonCommand | ✅ | |
| `ceph fs reset <name>` | MonCommand | ✅ | Reset to single-rank |
| `ceph fs rename <old> <new>` | MonCommand | ✅ | |
| `ceph fs status [name]` | MgrCommand | ✅ | High-level FS health |
| `ceph fs fail <name>` | MonCommand | ✅ | Immediately stops all MDS |
| `ceph fs authorize <name> <entity> <path> [rw]` | MonCommand | ✅ | |
| `ceph mds session ls` | MonCommand | ✅ | Active client sessions |
| `ceph mds session evict <id>` | MonCommand | ✅ | |
| `ceph fs perf stats` | MgrCommand | ✅ | Same data as `cephfs-top --dump`; use ExecMgr |
| `ceph mds compat show` | MonCommand | ✅ | |

### 1.12 Service & Version Info

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph service dump` | MonCommand | ✅ | Active services with endpoints |
| `ceph service status` | MonCommand | ✅ | |
| `ceph version` | MonCommand | ✅ | |
| `ceph mgr dump` | MonCommand | ✅ | Active/standby MGR info |
| `ceph mgr stat` | MonCommand | ✅ | |
| `ceph mgr module ls` | MgrCommand | ✅ | Enabled/disabled modules |
| `ceph mgr module enable <mod>` | MonCommand | ✅ | |
| `ceph mgr module disable <mod>` | MonCommand | ✅ | |
| `ceph mgr fail [mgr_id]` | MonCommand | ✅ | Forces MGR failover |
| `ceph mgr metadata [id]` | MonCommand | ✅ | |
| `ceph mgr count-metadata <prop>` | MonCommand | ✅ | |
| `ceph mgr versions` | MonCommand | ✅ | |

### 1.13 Device Health

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph device ls` | MgrCommand | ✅ | All known devices with health; does NOT require orchestrator |
| `ceph device ls-by-host <hostname>` | MgrCommand | ✅ | |
| `ceph device ls-by-daemon <daemon>` | MgrCommand | ✅ | |
| `ceph device get-health-metrics <devid>` | MgrCommand | ✅ | SMART data |
| `ceph device check-health` | MgrCommand | ✅ | Triggers health check |
| `ceph device monitoring on/off` | MgrCommand | ✅ | |
| `ceph device set-life-expectancy <devid> <from> [to]` | MgrCommand | ✅ | |
| `ceph device rm-life-expectancy <devid>` | MgrCommand | ✅ | |
| `ceph device predict-life-expectancy <devid>` | MgrCommand | ✅ | |

### 1.14 NVMe-oF Gateway

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `ceph nvme-gw create <gw_id> <pool> <group>` | MonCommand | ✅ | |
| `ceph nvme-gw delete <gw_id> <pool> <group>` | MonCommand | ✅ | |
| `ceph nvme-gw show <pool> [group]` | MonCommand | ✅ | |

---

## 2. `orch *` Commands (ExecMgr — conditional on orchestrator backend)

All `orch *` commands route through `ExecMgr`. Every call requires cephadm or rook configured as the backend, otherwise returns `-ENOENT` "No orchestrator configured" (source: `orchestrator/_interface.py:1899`).

| Group | Commands | Feasibility |
|-------|---------|------------|
| **Host management** | `host add/rm/drain/set-addr/ls/label add\|rm/ok-to-stop/maintenance enter\|exit/rescan` | 🔒 CONDITIONAL |
| **Hardware status** | `hardware status [hostname] [category]`, `hardware light`, `hardware powercycle`, `hardware shutdown` | 🔒 CONDITIONAL |
| **Device management** | `device ls [hostname]`, `device zap`, `device replace`, `device ls-lights`, `device light` | 🔒 CONDITIONAL |
| **Service lifecycle** | `ls`, `ps`, `start/stop/restart/redeploy/reconfig <svc>`, `daemon <action>`, `daemon rm`, `rm <svc>` | 🔒 CONDITIONAL |
| **Service apply (spec-based)** | `apply osd/mds/rgw/nfs/iscsi/nvmeof/snmp-gateway/mgmt-gateway/oauth2-proxy/jaeger/smb/…` | 🔒 CONDITIONAL |
| **Daemon add** | `daemon add osd/mds/rgw/nfs/iscsi/nvmeof` | 🔒 CONDITIONAL |
| **OSD removal** | `osd rm <ids>`, `osd rm stop`, `osd rm status`, `osd set-spec-affinity` | 🔒 CONDITIONAL |
| **Orchestrator control** | `set backend <mod>` (no backend needed), `status`, `pause`, `resume`, `cancel` | 🔒 / ✅ |
| **Upgrade** | `upgrade check/ls/start/status/pause/resume/stop`, `update service` | 🔒 CONDITIONAL |
| **Tuned profiles** | `tuned-profile apply/rm/ls/add-setting/rm-setting` | 🔒 CONDITIONAL |
| **Certificate manager** | `certmgr reload/cert ls/bindings ls/cert check/key ls/cert get/key get/cert-key set/…` | 🔒 CONDITIONAL |
| **Monitoring credentials** | `prometheus set-credentials/get-credentials/set-target/remove-target/set-custom-alerts` | 🔒 CONDITIONAL |
| **Client keyrings** | `client-keyring ls/set/rm` | 🔒 CONDITIONAL |

---

## 3. `cephadm *` MGR Commands (ExecMgr — all feasible)

These commands (`cephadm set-ssh-config`, `cephadm generate-key`, `cephadm check-host`, `cephadm registry-login`, etc.) are registered in the cephadm MGR module and dispatch via `ExecMgr`. All ~24 are FEASIBLE. Source: `src/pybind/mgr/cephadm/module.py`.

---

## 4. MGR Module Commands (ExecMgr — all feasible)

| Module | Commands | Feasibility |
|--------|---------|------------|
| **balancer** | `status/mode/on/off/pool ls\|add\|rm/eval/eval-verbose/optimize/show/rm/reset/dump/ls/execute` | ✅ All 17 |
| **snap_schedule** | `fs snap-schedule status/list/add/remove/retention add\|remove/activate/deactivate` | ✅ All 8 |
| **telemetry** | `status/diff/on/off/send/enable\|disable channel/channel ls/collection ls/show/preview/show-device/…` | ✅ All 17 |
| **alerts** | `alerts send` | ✅ |
| **fs volume/subvolume** | `fs volume ls/create/rm/rename/info`, `fs subvolumegroup *`, `fs subvolume *`, `fs clone *`, `fs quiesce *` | ✅ All ~55 (also available via go-ceph/cephfs/admin) |
| **fs perf stats** | `fs perf stats` | ✅ Same data as `cephfs-top --dump` |

---

## 5. `rbd` CLI (go-ceph/rbd)

### 5.1 Image Lifecycle — All FEASIBLE ✅

`rbd ls`, `create`, `rm`, `rename`, `resize`, `info`, `copy/cp`, `deep copy`, `flatten`, `sparsify`, `diff`, `status`, `watch`, `children`, `du/disk-usage`

go-ceph functions: `GetImageNames`, `CreateImage`, `image.Remove`, `image.Rename`, `image.Resize`, `image.Stat`, `image.Copy`, `image.Flatten`, `image.Sparsify`, `image.DiffIterate`, `image.ListWatchers`

### 5.2 Snapshots — All FEASIBLE ✅

`rbd snap list/create/remove/purge/rollback/protect/unprotect/limit set/limit clear/rename`, `rbd clone`

go-ceph: `image.GetSnapshotNames`, `image.CreateSnapshot`, `snap.Remove`, `snap.Rollback`, `snap.Protect`, `snap.Unprotect`, `image.Clone`

### 5.3 Import / Export — CONDITIONAL ⚠

| Command | Notes |
|---------|-------|
| `rbd export` | No single `rbd_export` C API. Must iterate DiffIterate + ReadAt in Go and stream binary `.rbd` format. Requires streaming HTTP endpoint (chunked transfer). |
| `rbd export-diff` | Same — incremental binary `.diff` format. go-ceph has all I/O primitives; binary framing must be reimplemented. |
| `rbd import` | Must parse `.rbd` binary format and apply `WriteAt` calls. Requires multipart upload. |
| `rbd import-diff` | Must parse `.diff` format. |
| `rbd merge-diff` | ❌ INFEASIBLE — pure local file merge of two `.diff` files; no librbd API; not appropriate for a service. |

### 5.4 Feature & Configuration — All FEASIBLE ✅

`rbd feature enable/disable`, `rbd config pool get/set/remove/list`, `rbd config image get/set/remove/list`, `rbd image-meta list/get/set/remove`, `rbd pool init`, `rbd pool stats`

### 5.5 Locks, Trash, Groups — All FEASIBLE ✅

`rbd lock list/add/remove`, `rbd trash move/remove/purge/list/restore`, `rbd trash purge schedule *`, `rbd group create/remove/list/rename/info/image add\|remove\|list/snap *`

### 5.6 Mirror — All FEASIBLE ✅

`rbd mirror image enable/disable/promote/demote/resync/status/snapshot`, `rbd mirror pool enable/disable/info/status/promote/demote/peer add\|remove\|set/peer bootstrap create\|import`, `rbd mirror snapshot schedule *`

### 5.7 Migration, Namespace, Object Map — All FEASIBLE ✅

`rbd migration prepare/execute/abort/commit`, `rbd namespace create/remove/list`, `rbd object-map rebuild/check`

### 5.8 Encryption — CONDITIONAL ⚠

`rbd encryption format` — `rbd_encryption_format` (LUKS1/LUKS2), go-ceph has binding. **Passphrase must never be in URL/query params; pass in request body or reference a secret.**

### 5.9 Performance & Bench — CONDITIONAL ⚠

| Command | Notes |
|---------|-------|
| `rbd bench` | All I/O primitives available. Must implement bench loop in Go. Results should be streamed (SSE or periodic JSON). |
| `rbd perf image iotop` | Calls `rados_mgr_command("rbd perf image stats")`. go-ceph `MgrCommand` available. Streaming output requires SSE or polling. |
| `rbd perf image iostat` | Same — one-shot FEASIBLE; streaming CONDITIONAL. |

### 5.10 Journal — INFEASIBLE ❌

`rbd journal info/status/reset/inspect/export/import/client disconnect` — uses internal `Journaler` class and `cls/journal` OSD class directly; not exposed via the librbd C API; no go-ceph binding.

### 5.11 Device/NBD — INFEASIBLE ❌

| Command | Reason |
|---------|--------|
| `rbd device list/map/unmap/attach/detach` | Interact with `/sys/bus/rbd/` — kernel RBD module. Requires root on the daemon host. |
| `rbd nbd map/unmap/list` | `rbd-nbd` daemon maps via userspace NBD to `/dev/nbdN`. Host-local, root required. |

---

## 6. `radosgw-admin` CLI

### 6.1 User & Auth Management — FEASIBLE via go-ceph/rgw/admin ✅

`user create/modify/info/rm/suspend/enable/stats/list`, `subuser create/modify/rm`, `key create/rm`, `caps add/rm`

**Exception:** `user check` — reads RADOS objects directly; ❌ INFEASIBLE. `user rename` — Admin HTTP exists but no go-ceph wrapper; ⚠ CONDITIONAL (direct HTTP call).

### 6.2 Bucket Operations

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `bucket list/link/unlink/stats/rm` | Admin HTTP → go-ceph | ✅ | go-ceph/rgw/admin |
| `bucket limit check` | Admin HTTP (stats + client analysis) | ⚠ | Fetches stats, does shard analysis; implementable |
| `bucket check` | Admin HTTP `/admin/bucket?check-objects=true` | ⚠ | Route exists; no go-ceph wrapper; direct HTTP call |
| `bucket sync disable/enable` | Admin HTTP `/admin/bucket?sync` | ⚠ | Route exists; no go-ceph wrapper |
| `bucket check olh` | CLI-only raw RADOS | ❌ | No Admin HTTP route |
| `bucket check unlinked` | CLI-only full index iteration | ❌ | No Admin HTTP route |
| `bucket chown` | CLI-only | ❌ | |
| `bucket reshard` | CLI-only `RGWBucketReshard` | ❌ | No `/admin/bucket?reshard` route |
| `bucket set-min-shards` | CLI-only | ❌ | |
| `bucket rewrite` | CLI-only | ❌ | |
| `bucket sync checkpoint` | CLI-only | ❌ | |
| `bucket radoslist` | CLI-only direct RADOS | ❌ | |
| `bucket logging flush/info/list` | CLI-only | ❌ | |

### 6.3 Quota & Rate Limiting

| Command | Route | Feasibility | Notes |
|---------|-------|-------------|-------|
| `quota set/enable/disable` (user/bucket) | Admin HTTP → go-ceph | ✅ | go-ceph/rgw/admin |
| `quota get` (individual bucket) | Admin HTTP | ✅ | |
| `global quota get/set` | CLI-only; modifies zone config | ❌ | |
| `ratelimit get/set/enable/disable` (user/bucket) | Admin HTTP `/admin/ratelimit` | ⚠ | Route exists; no go-ceph wrapper; direct HTTP call |
| `global ratelimit` | CLI-only | ❌ | |

### 6.4 GC & Lifecycle — Mixed

| Command | Feasibility | Notes |
|---------|-------------|-------|
| `gc list` | ❌ INFEASIBLE | Reads GC omap via `cls_rgw_gc` directly; no Admin HTTP route |
| `gc process` | ❌ INFEASIBLE | Calls `RGWGC::process()` locally; no Admin HTTP route |
| `lc list` | ❌ INFEASIBLE | Reads LC index omap; no Admin HTTP route |
| `lc get` | ⚠ CONDITIONAL | Standard S3 `GET /bucket?lifecycle` |
| `lc process` | ❌ INFEASIBLE | Triggers LC engine locally; no Admin HTTP route |

### 6.5 Object Operations — Mixed

| Command | Feasibility | Notes |
|---------|-------------|-------|
| `object rm` | ⚠ CONDITIONAL | Admin HTTP route exists; no go-ceph wrapper |
| `object put` | ⚠ CONDITIONAL | Standard S3 PUT |
| `object stat` | ⚠ CONDITIONAL | Standard S3 HEAD |
| `object unlink/rewrite/reindex/manifest` | ❌ INFEASIBLE | No Admin HTTP route |

### 6.6 Usage & Logs

| Command | Feasibility | Notes |
|---------|-------------|-------|
| `usage show/trim/clear` | ✅ | go-ceph/rgw/admin |
| `log list/show/rm` | ⚠ CONDITIONAL | Admin HTTP `/admin/log` route exists; no go-ceph wrapper; direct HTTP call |
| `mdlog/bilog/datalog` | ⚠ CONDITIONAL | Admin HTTP `/admin/log?type=mdlog/bilog/datalog`; no go-ceph wrapper |

### 6.7 Multi-Site (Zone / Zonegroup / Realm / Period)

| Command | Feasibility | Notes |
|---------|-------------|-------|
| `zone get/set/create/modify/delete/list/rename` | ❌ INFEASIBLE | No `/admin/zone` Admin HTTP route; modifies zone config locally. Workaround: some zone reads accessible via `zonegroup get` |
| `zonegroup get/create/modify/delete/list` | ❌ INFEASIBLE | Same — CLI-only |
| `realm get/create/delete/list/pull` | ❌ INFEASIBLE | CLI-only |
| `period update/commit/get/list` | ❌ INFEASIBLE | CLI-only |
| `sync info/status` | ❌ INFEASIBLE | Calls `sync_info()` / `sync_status()` which use local coroutine API |
| `metadata sync status` | ❌ INFEASIBLE | `RGWMetaSyncStatusManager` — local |
| `data sync status` | ❌ INFEASIBLE | `RGWDataSyncStatusManager` — local |
| `bucket sync info/status` | ❌ INFEASIBLE | Local coroutine state |

### 6.8 Role — CONDITIONAL ⚠

`role create/delete/get/list/modify/add-policy/remove-policy/list-attached-policies`: IAM REST API (`/?Action=CreateRole` etc.) — not in Admin API; requires aws-sdk-go-v2 or direct HTTP to IAM endpoint.

### 6.9 MFA / OTP — INFEASIBLE ❌

`mfa create/remove/get/list/check/resync`, `otp check/create/delete/list/show`: **Source-verified INFEASIBLE.** These commands write directly to RADOS OTP objects via `rgw_otp.cc`. No `/admin/user?mfa` route exists in the registered Admin HTTP route table (`src/rgw/driver/rados/rgw_sal_rados.cc:2568`, `src/rgw/rgw_appmain.cc:347`). This was a research correction from initial estimate.

**Note on `rgw admin <args>` MgrCommand:** The MGR `rgw` module exposes a proxy MgrCommand (`"prefix":"rgw admin"`) that forwards raw args to the active RGW daemon via `ExecMgr`. This is useful for some RGW operations, but does NOT bypass the Admin HTTP route restriction — commands that operate on local RADOS objects (gc, lc, bi, mfa, bucket reshard) remain INFEASIBLE even via this proxy.

---

## 7. `rados` CLI (go-ceph/rados IoCtx)

### 7.1 Pool-Level — All FEASIBLE ✅

`rados lspools`, `rados df`, `rados lssnap/mksnap/rmsnap`

**Exception:** `rados cppool`, `rados purge` — ⚠ CONDITIONAL: no atomic C API; must implement as object-by-object iteration in Go.

### 7.2 Object I/O — All FEASIBLE ✅

`rados ls/get/put/append/truncate/create/rm/cp/stat/stat2/touch/rollback/listsnaps`

go-ceph functions: `ioctx.ListObjects`, `ioctx.Read`, `ioctx.Write`, `ioctx.Append`, `ioctx.Truncate`, `ioctx.Delete`, `ioctx.Stat`

For `get/put`: binary streaming required (chunked HTTP, not JSON response body).

### 7.3 OMAP Operations — All FEASIBLE ✅

`rados getomapval/setomapval/rmomapkey/getomapheader/setomapheader/clearomap/listomapkeys/listomapvals`

go-ceph: `ioctx.GetOmapValues`, `ioctx.SetOmap`, `ioctx.RmOmapKeys`, `ioctx.GetOmapHeader`, `ioctx.SetOmapHeader`, `ioctx.CleanOmap`

### 7.4 Locking — All FEASIBLE ✅

`rados lock exclusive/shared`, `rados lock info`, `rados lock list`, `rados lock break`, `rados unlock`

go-ceph `rados/objclass` or `ioctx` lock ops: `ioctx.LockExclusive`, `ioctx.LockShared`, `ioctx.ListLockers`, `ioctx.BreakLock`, `ioctx.Unlock`

### 7.5 Exec (OSD class calls) — CONDITIONAL ⚠

`rados <pool> <object> <class> <method>`: `ioctx.Exec()` in go-ceph wraps `rados_exec`. Feasible, but exposing arbitrary OSD class execution is a security risk — scope carefully.

### 7.6 Watch / Notify — CONDITIONAL ⚠

`rados watch <pool> <object>` / `rados notify`: `ioctx.Watch2()`, `ioctx.Notify2()` in go-ceph. Requires a streaming endpoint (SSE or WebSocket) — not a simple JSON response.

### 7.7 Bench — CONDITIONAL ⚠

`rados bench <seconds> write/seq/rand`: All I/O primitives available. Must implement the benchmark loop in Go and stream results (periodic JSON updates). Not fire-and-forget.

### 7.8 Import / Export — CONDITIONAL ⚠

`rados export <pool>` / `rados import <pool> <path>`: Iterates all objects and produces a custom binary format. go-ceph has all I/O primitives but the binary framing must be reimplemented. Requires streaming binary upload/download.

---

## 8. CephFS Tools

### 8.1 `cephfs-top` — FEASIBLE ✅

`cephfs-top --dump` calls `ExecMgr({"prefix": "fs perf stats"})` internally. Call the same MGR command directly from ceph-api. **Do not run the tool; call the MGR command.**

### 8.2 `cephfs-shell` — ❌ INFEASIBLE (as interactive tool)

Mounts CephFS via libcephfs and runs POSIX operations interactively. Individual operations are feasible via `go-ceph/cephfs` mount API, but the interactive shell itself cannot be proxied. Implement individual FS operations (ls, mkdir, stat, etc.) as gRPC methods if needed.

### 8.3 `cephfs-journal-tool` — ❌ INFEASIBLE

Reads/writes MDS journal RADOS objects directly. Requires stopped MDS. No MGR/Mon command equivalent. Used only for offline recovery.

### 8.4 `cephfs-table-tool` — ❌ INFEASIBLE

Reads/writes raw MDS SessionMap, SnapServer, InoTable RADOS objects. Offline recovery only.

### 8.5 `cephfs-data-scan` — ❌ INFEASIBLE

Full metadata recovery tool. Requires direct RADOS pool enumeration and a stopped MDS. All 7 subcommands (init, scan_extents, scan_inodes, scan_frags, scan_links, pg_files, cleanup) are offline-only.

---

## 9. `ceph daemon` / `ceph tell` — INFEASIBLE ❌

Both commands connect to the daemon's Unix domain socket at `/var/run/ceph/<cluster>-<id>.asok`. There is no remote transport in librados for admin socket messages.

### Commands exclusively available via admin socket

| Command | Daemon | Why no remote equivalent |
|---------|--------|--------------------------|
| `perf dump` | All | Full PerfCounter values at this moment. Different from `ceph osd perf` (which gives only latency snapshot). Rolling window is in MGR heap. |
| `perf schema` | All | Returns schema of all perf counters |
| `perf reset [name]` | All | Resets a counter |
| `dump_historic_ops` | OSD | Slowest recent ops with full timing breakdown. In-memory ring buffer in OSD process. |
| `dump_historic_slow_ops` | OSD | Subset of dump_historic_ops |
| `dump_ops_in_flight` | OSD | Currently executing ops |
| `ops` | OSD | Inflight op summary |
| `config show` | All | Live runtime config (may differ from committed config) |
| `config set <key> <val>` | All | Runtime config change without Monitor commit |
| `config get <key>` | All | Live runtime value |
| `config diff` | All | Diff from defaults |
| `config unset <key>` | All | Remove runtime override |
| `flush_journal` | MDS | Flush journal to backing store |
| `dump_journal` | MDS | |
| `session ls` | MDS | Live client session list |
| `session evict <id>` | MDS | Live session evict |
| `get_subtrees` | MDS | MDS subtree map |
| `dump_blocked_ops` | MDS | Blocked ops |
| `status` | MGR | MGR module status, Python errors |
| `cache drop` | MGR | Release Python process caches |
| `log flush` | All | Flush log buffers to disk |
| `help` | All | List all admin socket commands |
| `version` | All | Exact daemon version string |

**Workaround:** `ceph-exporter` daemon (`:9926`) reads each daemon's admin socket directly and exposes the data as Prometheus metrics. Scraping `:9926` gives access to perf counters without admin socket access.

---

## 10. `ceph-volume` CLI — INFEASIBLE ❌

`ceph-volume` is a host-local LVM/BlueStore OSD provisioning tool. All subcommands (`lvm create`, `lvm activate`, `lvm list`, `lvm prepare`, `lvm zap`, `raw prepare`, `raw activate`, `inventory`) require host-local execution with root privileges. No Mon/MGR API surface exists.

**Partial workaround:** `orch apply osd` (via ExecMgr) triggers cephadm to run `ceph-volume` on target hosts over SSH, but requires the orchestrator backend to be configured.

---

## 11. `cephadm` (direct binary) — Mostly INFEASIBLE ❌

| Subcommand | Feasibility | Remote equivalent |
|------------|-------------|-------------------|
| `bootstrap` | ❌ | None — one-time cluster init |
| `deploy <daemon>` | 🔒 via `orch daemon add` | ExecMgr |
| `ls` | 🔒 via `orch ps` | ExecMgr |
| `check-host` | 🔒 via `cephadm check-host` (MGR cmd) | ExecMgr |
| `prepare-host` | 🔒 via `cephadm prepare-host` (MGR cmd) | ExecMgr |
| `rm-daemon` | 🔒 via `orch daemon rm` | ExecMgr |
| `rm-cluster` | ❌ | None |
| `shell` / `enter` | ❌ | None — container shell access |
| `logs <daemon>` | ❌ | None — streams container logs from host |
| `pull [--image]` | ❌ | `orch upgrade start` pulls images (different scope) |
| `inspect-image` | ❌ | None |
| `list-networks` | ❌ | None |
| `adopt` | ❌ | None — legacy migration |
| `run <daemon>` | ❌ | None — foreground daemon run |
| `signal <daemon> <sig>` | ❌ | None |
| `add-repo/rm-repo/install` | ❌ | None — host package management |
| `registry-login` | 🔒 via `cephadm registry-login` (MGR cmd) | ExecMgr |
| `gather-facts` | 🔒 via `orch host ls --detail` | ExecMgr (indirect) |
| `maintenance enter/exit` | 🔒 via `orch host maintenance enter/exit` | ExecMgr |
| `disk-rescan <host>` | 🔒 via `orch host rescan` | ExecMgr |
| `zap-osds` | 🔒 via `orch device zap` | ExecMgr |
| `unit <daemon> <action>` | ⚠ partial | `orch daemon <action>` covers daemon-level; direct systemd unit control is host-local |
| `agent` | ❌ | Internal node-proxy agent daemon |
| `list-images` | ❌ | `orch upgrade ls` is partial |
| `update-service` | 🔒 via `orch update service` (MGR cmd) | ExecMgr |
| `version` | ✅ | `ceph version` (ExecMon) |

---

## Summary Statistics

| CLI Tool | Total command groups | ✅ FEASIBLE | ⚠ CONDITIONAL | 🔒 CONDITIONAL (backend) | ❌ INFEASIBLE |
|----------|---------------------|------------|----------------|--------------------------|--------------|
| `ceph` (mon/mgr) | ~180 | ~160 (~89%) | ~12 (binary output, inbuf) | — | ~8 (tell/daemon/ping/-w) |
| `orch *` | ~75 | — | — | ~74 (~99%) | 1 (`orch status` w/o backend) |
| `cephadm *` (MGR module) | 24 | 24 (100%) | — | — | 0 |
| `balancer/snap_schedule/telemetry/alerts/fs` | ~100 | ~100 (100%) | — | — | 0 |
| `rbd` | ~85 | ~72 (~85%) | ~10 (streaming, encryption) | — | ~3 (journal, device map/nbd) |
| `radosgw-admin` | ~90 | ~30 (~33%) | ~25 (~28%) | — | ~35 (~39%) |
| `rados` | ~35 | ~28 (~80%) | ~7 (streaming, bench) | — | 0 |
| `ceph daemon` / `ceph tell` | ~30+ per-daemon | 0 | 0 | — | 100% |
| `ceph-volume` | ~15 | 0 | 0 | — | 100% |
| `cephadm` (direct) | ~30 | 1 (version) | 2 (systemd unit) | ~13 (via orch) | ~14 |
| `cephfs-top` | 2 | 2 (100%) | — | — | 0 |
| `cephfs-shell` | N/A | per-op via go-ceph/cephfs | — | — | as interactive shell |
| `cephfs offline tools` | ~23 | 0 | 0 | — | 100% |
