# Endpoints to migrate

Grouped by resource (create/POST first in each group). Tiers ordered by
popularity + similarity to already-implemented endpoints (mon/mgr commands
over rados — same as cluster, crush_rule, status, users, roles).

## Tier 1 — popular Ceph, mon/mgr commands

### pool
- [x] POST /api/pool
- [x] GET /api/pool  (**PARTIAL!**: `stats=true` → ErrNotImplemented (needs mgr time-series cache); `attrs` field-whitelist not honored — see task §Open decisions)
- [ ] GET /api/pool/{pool_name}
- [ ] PUT /api/pool/{pool_name}
- [ ] DELETE /api/pool/{pool_name}
- [ ] GET /api/pool/{pool_name}/configuration

### erasure_code_profile
- [ ] POST /api/erasure_code_profile
- [ ] GET /api/erasure_code_profile
- [ ] GET /api/erasure_code_profile/{name}
- [ ] DELETE /api/erasure_code_profile/{name}

### osd
- [ ] POST /api/osd
- [ ] GET /api/osd
- [ ] GET /api/osd/{svc_id}
- [ ] PUT /api/osd/{svc_id}
- [ ] DELETE /api/osd/{svc_id}
- [ ] POST /api/osd/{svc_id}/destroy
- [ ] POST /api/osd/{svc_id}/purge
- [ ] POST /api/osd/{svc_id}/reweight
- [ ] POST /api/osd/{svc_id}/scrub
- [ ] PUT /api/osd/{svc_id}/mark
- [ ] GET /api/osd/{svc_id}/devices
- [ ] GET /api/osd/{svc_id}/histogram
- [ ] GET /api/osd/{svc_id}/smart
- [ ] GET /api/osd/flags
- [ ] PUT /api/osd/flags
- [ ] GET /api/osd/flags/individual
- [ ] PUT /api/osd/flags/individual
- [ ] GET /api/osd/safe_to_delete
- [ ] GET /api/osd/safe_to_destroy
- [ ] GET /api/osd/settings

### cluster_conf
- [ ] POST /api/cluster_conf
- [ ] GET /api/cluster_conf
- [ ] GET /api/cluster_conf/{name}
- [ ] GET /api/cluster_conf/filter
- [ ] PUT /api/cluster_conf
- [ ] DELETE /api/cluster_conf/{name}

### mgr/module
- [ ] GET /api/mgr/module
- [ ] GET /api/mgr/module/{module_name}
- [ ] PUT /api/mgr/module/{module_name}
- [ ] GET /api/mgr/module/{module_name}/options
- [ ] POST /api/mgr/module/{module_name}/enable
- [ ] POST /api/mgr/module/{module_name}/disable

### monitor
- [ ] GET /api/monitor

### health
- [ ] GET /api/health/minimal
- [ ] GET /api/health/full
- [ ] GET /api/health/get_cluster_capacity
- [ ] GET /api/health/get_cluster_fsid
- [ ] GET /api/health/get_telemetry_status

### summary
- [ ] GET /api/summary

### logs
- [ ] GET /api/logs/all

## Tier 2 — CephFS (mgr `fs`/volumes commands — same rados primitives)

### cephfs
- [ ] POST /api/cephfs
- [ ] GET /api/cephfs
- [ ] GET /api/cephfs/{fs_id}
- [ ] PUT /api/cephfs/auth
- [ ] PUT /api/cephfs/rename
- [ ] DELETE /api/cephfs/remove/{name}
- [ ] GET /api/cephfs/{fs_id}/clients
- [ ] DELETE /api/cephfs/{fs_id}/client/{client_id}
- [ ] GET /api/cephfs/{fs_id}/get_root_directory
- [ ] GET /api/cephfs/{fs_id}/ls_dir
- [ ] GET /api/cephfs/{fs_id}/mds_counters
- [ ] GET /api/cephfs/{fs_id}/quota
- [ ] PUT /api/cephfs/{fs_id}/quota
- [ ] PUT /api/cephfs/{fs_id}/rename-path
- [ ] GET /api/cephfs/{fs_id}/statfs
- [ ] POST /api/cephfs/{fs_id}/snapshot
- [ ] DELETE /api/cephfs/{fs_id}/snapshot
- [ ] POST /api/cephfs/{fs_id}/tree
- [ ] DELETE /api/cephfs/{fs_id}/tree
- [ ] DELETE /api/cephfs/{fs_id}/unlink
- [ ] POST /api/cephfs/{fs_id}/write_to_file

### cephfs/subvolume/group
- [ ] POST /api/cephfs/subvolume/group
- [ ] GET /api/cephfs/subvolume/group/{vol_name}
- [ ] PUT /api/cephfs/subvolume/group/{vol_name}
- [ ] DELETE /api/cephfs/subvolume/group/{vol_name}
- [ ] GET /api/cephfs/subvolume/group/{vol_name}/info

### cephfs/subvolume
- [ ] POST /api/cephfs/subvolume
- [ ] GET /api/cephfs/subvolume/{vol_name}
- [ ] PUT /api/cephfs/subvolume/{vol_name}
- [ ] DELETE /api/cephfs/subvolume/{vol_name}
- [ ] GET /api/cephfs/subvolume/{vol_name}/exists
- [ ] GET /api/cephfs/subvolume/{vol_name}/info

### cephfs/subvolume/snapshot
- [ ] POST /api/cephfs/subvolume/snapshot
- [ ] GET /api/cephfs/subvolume/snapshot/{vol_name}/{subvol_name}
- [ ] DELETE /api/cephfs/subvolume/snapshot/{vol_name}/{subvol_name}
- [ ] GET /api/cephfs/subvolume/snapshot/{vol_name}/{subvol_name}/info
- [ ] POST /api/cephfs/subvolume/snapshot/clone

### cephfs/snapshot/schedule
- [ ] POST /api/cephfs/snapshot/schedule
- [ ] GET /api/cephfs/snapshot/schedule/{fs}
- [ ] PUT /api/cephfs/snapshot/schedule/{fs}/{path}
- [ ] POST /api/cephfs/snapshot/schedule/{fs}/{path}/activate
- [ ] POST /api/cephfs/snapshot/schedule/{fs}/{path}/deactivate
- [ ] DELETE /api/cephfs/snapshot/schedule/{fs}/{path}/delete_snapshot

## Tier 3 — RBD / block (librbd)

### block/image
- [ ] POST /api/block/image
- [ ] GET /api/block/image
- [ ] GET /api/block/image/{image_spec}
- [ ] PUT /api/block/image/{image_spec}
- [ ] DELETE /api/block/image/{image_spec}
- [ ] GET /api/block/image/default_features
- [ ] GET /api/block/image/clone_format_version
- [ ] POST /api/block/image/{image_spec}/copy
- [ ] POST /api/block/image/{image_spec}/flatten
- [ ] POST /api/block/image/{image_spec}/move_trash

### block/image/snap
- [ ] POST /api/block/image/{image_spec}/snap
- [ ] PUT /api/block/image/{image_spec}/snap/{snapshot_name}
- [ ] DELETE /api/block/image/{image_spec}/snap/{snapshot_name}
- [ ] POST /api/block/image/{image_spec}/snap/{snapshot_name}/clone
- [ ] POST /api/block/image/{image_spec}/snap/{snapshot_name}/rollback

### block/image/trash
- [ ] GET /api/block/image/trash
- [ ] POST /api/block/image/trash/purge
- [ ] DELETE /api/block/image/trash/{image_id_spec}
- [ ] POST /api/block/image/trash/{image_id_spec}/restore

### block/pool/namespace
- [ ] POST /api/block/pool/{pool_name}/namespace
- [ ] GET /api/block/pool/{pool_name}/namespace
- [ ] DELETE /api/block/pool/{pool_name}/namespace/{namespace}

### block/mirroring
- [ ] GET /api/block/mirroring/summary
- [ ] GET /api/block/mirroring/site_name
- [ ] PUT /api/block/mirroring/site_name
- [ ] GET /api/block/mirroring/pool/{pool_name}
- [ ] PUT /api/block/mirroring/pool/{pool_name}
- [ ] POST /api/block/mirroring/pool/{pool_name}/bootstrap/peer
- [ ] POST /api/block/mirroring/pool/{pool_name}/bootstrap/token
- [ ] POST /api/block/mirroring/pool/{pool_name}/peer
- [ ] GET /api/block/mirroring/pool/{pool_name}/peer
- [ ] GET /api/block/mirroring/pool/{pool_name}/peer/{peer_uuid}
- [ ] PUT /api/block/mirroring/pool/{pool_name}/peer/{peer_uuid}
- [ ] DELETE /api/block/mirroring/pool/{pool_name}/peer/{peer_uuid}

## Tier 4 — RGW (admin-ops HTTP API)

### rgw/bucket
- [ ] POST /api/rgw/bucket
- [ ] GET /api/rgw/bucket
- [ ] GET /api/rgw/bucket/{bucket}
- [ ] PUT /api/rgw/bucket/{bucket}
- [ ] DELETE /api/rgw/bucket/{bucket}
- [ ] GET /api/rgw/bucket/getEncryption
- [ ] GET /api/rgw/bucket/getEncryptionConfig
- [ ] PUT /api/rgw/bucket/setEncryptionConfig
- [ ] DELETE /api/rgw/bucket/deleteEncryption

### rgw/user
- [ ] POST /api/rgw/user
- [ ] GET /api/rgw/user
- [ ] GET /api/rgw/user/{uid}
- [ ] PUT /api/rgw/user/{uid}
- [ ] DELETE /api/rgw/user/{uid}
- [ ] GET /api/rgw/user/get_emails
- [ ] POST /api/rgw/user/{uid}/capability
- [ ] DELETE /api/rgw/user/{uid}/capability
- [ ] POST /api/rgw/user/{uid}/key
- [ ] DELETE /api/rgw/user/{uid}/key
- [ ] GET /api/rgw/user/{uid}/quota
- [ ] PUT /api/rgw/user/{uid}/quota
- [ ] POST /api/rgw/user/{uid}/subuser
- [ ] DELETE /api/rgw/user/{uid}/subuser/{subuser}

### rgw/daemon
- [ ] GET /api/rgw/daemon
- [ ] GET /api/rgw/daemon/{svc_id}
- [ ] PUT /api/rgw/daemon/set_multisite_config

### rgw/site
- [ ] GET /api/rgw/site

### rgw/realm
- [ ] POST /api/rgw/realm
- [ ] GET /api/rgw/realm
- [ ] GET /api/rgw/realm/{realm_name}
- [ ] PUT /api/rgw/realm/{realm_name}
- [ ] DELETE /api/rgw/realm/{realm_name}
- [ ] GET /api/rgw/realm/get_all_realms_info
- [ ] GET /api/rgw/realm/get_realm_tokens
- [ ] POST /api/rgw/realm/import_realm_token

### rgw/zonegroup
- [ ] POST /api/rgw/zonegroup
- [ ] GET /api/rgw/zonegroup
- [ ] GET /api/rgw/zonegroup/{zonegroup_name}
- [ ] PUT /api/rgw/zonegroup/{zonegroup_name}
- [ ] DELETE /api/rgw/zonegroup/{zonegroup_name}
- [ ] GET /api/rgw/zonegroup/get_all_zonegroups_info

### rgw/zone
- [ ] POST /api/rgw/zone
- [ ] GET /api/rgw/zone
- [ ] GET /api/rgw/zone/{zone_name}
- [ ] PUT /api/rgw/zone/{zone_name}
- [ ] DELETE /api/rgw/zone/{zone_name}
- [ ] PUT /api/rgw/zone/create_system_user
- [ ] GET /api/rgw/zone/get_all_zones_info
- [ ] GET /api/rgw/zone/get_pool_names
- [ ] GET /api/rgw/zone/get_user_list

### rgw/roles
- [ ] POST /api/rgw/roles
- [ ] GET /api/rgw/roles
- [ ] PUT /api/rgw/roles
- [ ] DELETE /api/rgw/roles/{role_name}

### rgw/multisite
- [ ] GET /api/rgw/multisite/sync_status
- [ ] GET /api/rgw/multisite/sync-policy
- [ ] POST /api/rgw/multisite/sync-policy-group
- [ ] GET /api/rgw/multisite/sync-policy-group/{group_id}
- [ ] PUT /api/rgw/multisite/sync-policy-group
- [ ] DELETE /api/rgw/multisite/sync-policy-group/{group_id}
- [ ] PUT /api/rgw/multisite/sync-flow
- [ ] DELETE /api/rgw/multisite/sync-flow/{flow_id}/{flow_type}/{group_id}
- [ ] PUT /api/rgw/multisite/sync-pipe
- [ ] DELETE /api/rgw/multisite/sync-pipe/{group_id}/{pipe_id}

## Tier 5 — orchestrator / cephadm (needs orchestrator backend)

### host
- [ ] POST /api/host
- [ ] GET /api/host
- [ ] GET /api/host/{hostname}
- [ ] PUT /api/host/{hostname}
- [ ] DELETE /api/host/{hostname}
- [ ] GET /api/host/{hostname}/daemons
- [ ] GET /api/host/{hostname}/devices
- [ ] GET /api/host/{hostname}/inventory
- [ ] GET /api/host/{hostname}/smart
- [ ] POST /api/host/{hostname}/identify_device

### service
- [ ] POST /api/service
- [ ] GET /api/service
- [ ] GET /api/service/{service_name}
- [ ] PUT /api/service/{service_name}
- [ ] DELETE /api/service/{service_name}
- [ ] GET /api/service/{service_name}/daemons
- [ ] GET /api/service/known_types

### daemon
- [ ] GET /api/daemon
- [ ] PUT /api/daemon/{daemon_name}

### cluster/upgrade
- [ ] POST /api/cluster/upgrade/start
- [ ] GET /api/cluster/upgrade
- [ ] GET /api/cluster/upgrade/status
- [ ] PUT /api/cluster/upgrade/pause
- [ ] PUT /api/cluster/upgrade/resume
- [ ] PUT /api/cluster/upgrade/stop

## Tier 6 — monitoring (mgr perf data / external Prometheus)

### perf_counters
- [ ] GET /api/perf_counters
- [ ] GET /api/perf_counters/mds/{service_id}
- [ ] GET /api/perf_counters/mon/{service_id}
- [ ] GET /api/perf_counters/mgr/{service_id}
- [ ] GET /api/perf_counters/osd/{service_id}
- [ ] GET /api/perf_counters/rgw/{service_id}
- [ ] GET /api/perf_counters/rbd-mirror/{service_id}
- [ ] GET /api/perf_counters/tcmu-runner/{service_id}

### prometheus
- [ ] GET /api/prometheus
- [ ] GET /api/prometheus/data
- [ ] GET /api/prometheus/rules
- [ ] GET /api/prometheus/alertgroup
- [ ] GET /api/prometheus/notifications
- [ ] GET /api/prometheus/silences
- [ ] POST /api/prometheus/silence
- [ ] DELETE /api/prometheus/silence/{s_id}

## Tier 7 — niche Ceph services (gateway/module backends)

### nfs-ganesha
- [ ] POST /api/nfs-ganesha/export
- [ ] GET /api/nfs-ganesha/export
- [ ] GET /api/nfs-ganesha/export/{cluster_id}/{export_id}
- [ ] PUT /api/nfs-ganesha/export/{cluster_id}/{export_id}
- [ ] DELETE /api/nfs-ganesha/export/{cluster_id}/{export_id}
- [ ] GET /api/nfs-ganesha/cluster

### iscsi
- [ ] POST /api/iscsi/target
- [ ] GET /api/iscsi/target
- [ ] GET /api/iscsi/target/{target_iqn}
- [ ] PUT /api/iscsi/target/{target_iqn}
- [ ] DELETE /api/iscsi/target/{target_iqn}
- [ ] GET /api/iscsi/discoveryauth
- [ ] PUT /api/iscsi/discoveryauth

### nvmeof
- [ ] POST /api/nvmeof/subsystem
- [ ] GET /api/nvmeof/subsystem
- [ ] GET /api/nvmeof/subsystem/{nqn}
- [ ] DELETE /api/nvmeof/subsystem/{nqn}
- [ ] GET /api/nvmeof/subsystem/{nqn}/connection
- [ ] POST /api/nvmeof/subsystem/{nqn}/host
- [ ] GET /api/nvmeof/subsystem/{nqn}/host
- [ ] DELETE /api/nvmeof/subsystem/{nqn}/host/{host_nqn}
- [ ] POST /api/nvmeof/subsystem/{nqn}/listener
- [ ] GET /api/nvmeof/subsystem/{nqn}/listener
- [ ] DELETE /api/nvmeof/subsystem/{nqn}/listener/{host_name}/{traddr}
- [ ] POST /api/nvmeof/subsystem/{nqn}/namespace
- [ ] GET /api/nvmeof/subsystem/{nqn}/namespace
- [ ] GET /api/nvmeof/subsystem/{nqn}/namespace/{nsid}
- [ ] PATCH /api/nvmeof/subsystem/{nqn}/namespace/{nsid}
- [ ] DELETE /api/nvmeof/subsystem/{nqn}/namespace/{nsid}
- [ ] GET /api/nvmeof/subsystem/{nqn}/namespace/{nsid}/io_stats
- [ ] GET /api/nvmeof/gateway
- [ ] GET /api/nvmeof/gateway/group
- [ ] GET /api/nvmeof/gateway/version
- [ ] GET /api/nvmeof/gateway/log_level
- [ ] PUT /api/nvmeof/gateway/log_level
- [ ] GET /api/nvmeof/spdk/log_level
- [ ] PUT /api/nvmeof/spdk/log_level
- [ ] PUT /api/nvmeof/spdk/log_level/disable

## Tier 8 — misc dashboard-only (settings, telemetry, UI helpers)

### settings
- [ ] GET /api/settings
- [ ] GET /api/settings/{name}
- [ ] PUT /api/settings
- [ ] PUT /api/settings/{name}
- [ ] DELETE /api/settings/{name}

### telemetry
- [ ] PUT /api/telemetry
- [ ] GET /api/telemetry/report

### task
- [ ] GET /api/task

### feature_toggles
- [ ] GET /api/feature_toggles

### grafana
- [ ] POST /api/grafana/dashboards
- [ ] GET /api/grafana/url
- [ ] GET /api/grafana/validation/{params}

### feedback
- [ ] POST /api/feedback
- [ ] GET /api/feedback
- [ ] POST /api/feedback/api_key
- [ ] GET /api/feedback/api_key
- [ ] DELETE /api/feedback/api_key

### misc (leftover single endpoints on already-implemented resources)
- [ ] POST /api/user/validate_password
- [ ] POST /api/role/{name}/clone
