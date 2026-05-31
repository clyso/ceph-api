package user

import "encoding/json"

var (
	systemRoles []Role = func() []Role {
		var res []Role
		err := json.Unmarshal([]byte(systemRolesJSON), &res)
		if err != nil {
			panic(err)
		}
		return res
	}()
	systemRoleMap map[string]Role = func() map[string]Role {
		res := make(map[string]Role, len(systemRoles))
		for i, v := range systemRoles {
			res[v.Name] = systemRoles[i]
		}
		return res
	}()
	permissionSet = map[string]Permission{
		"read":   PermRead,
		"create": PermCreate,
		"update": PermUpdate,
		"delete": PermDelete,
	}
	scopeSet = map[string]struct{}{
		"hosts":              {},
		"config-opt":         {},
		"pool":               {},
		"osd":                {},
		"monitor":            {},
		"rbd-image":          {},
		"iscsi":              {},
		"rbd-mirroring":      {},
		"rgw":                {},
		"cephfs":             {},
		"manager":            {},
		"log":                {},
		"grafana":            {},
		"prometheus":         {},
		"user":               {},
		"dashboard-settings": {},
		"nfs-ganesha":        {},
		"nvme-of":            {},
	}
)

type Scope string

const (
	ScopeHosts             Scope = "hosts"
	ScopeConfigOpt         Scope = "config-opt"
	ScopePool              Scope = "pool"
	ScopeOsd               Scope = "osd"
	ScopeMonitor           Scope = "monitor"
	ScopeRbdImage          Scope = "rbd-image"
	ScopeIscsi             Scope = "iscsi"
	ScopeRbdMirroring      Scope = "rbd-mirroring"
	ScopeRgw               Scope = "rgw"
	ScopeCephfs            Scope = "cephfs"
	ScopeManager           Scope = "manager"
	ScopeLog               Scope = "log"
	ScopeGrafana           Scope = "grafana"
	ScopePrometheus        Scope = "prometheus"
	ScopeUser              Scope = "user"
	ScopeDashboardSettings Scope = "dashboard-setting"
	ScopeNfsGanesha        Scope = "nfs-ganesha"
	ScopeNvmeOf            Scope = "nvme-of"
)

type Permission uint8

func (p Permission) String() string {
	return permissionList[int(p)]
}

const (
	PermRead Permission = iota
	PermCreate
	PermUpdate
	PermDelete
)

var permissionList = []string{"read", "create", "update", "delete"}

const (
	systemRolesJSON = `[
		{
			"name": "administrator",
			"description": "allows full permissions for all security scopes",
			"scopes_permissions": {
				"cephfs": [
					"create",
					"delete",
					"read",
					"update"
				],
				"config-opt": [
					"create",
					"delete",
					"read",
					"update"
				],
				"dashboard-settings": [
					"create",
					"delete",
					"read",
					"update"
				],
				"grafana": [
					"create",
					"delete",
					"read",
					"update"
				],
				"hosts": [
					"create",
					"delete",
					"read",
					"update"
				],
				"iscsi": [
					"create",
					"delete",
					"read",
					"update"
				],
				"log": [
					"create",
					"delete",
					"read",
					"update"
				],
				"manager": [
					"create",
					"delete",
					"read",
					"update"
				],
				"monitor": [
					"create",
					"delete",
					"read",
					"update"
				],
				"nfs-ganesha": [
					"create",
					"delete",
					"read",
					"update"
				],
				"nvme-of": [
					"create",
					"delete",
					"read",
					"update"
				],
				"osd": [
					"create",
					"delete",
					"read",
					"update"
				],
				"pool": [
					"create",
					"delete",
					"read",
					"update"
				],
				"prometheus": [
					"create",
					"delete",
					"read",
					"update"
				],
				"rbd-image": [
					"create",
					"delete",
					"read",
					"update"
				],
				"rbd-mirroring": [
					"create",
					"delete",
					"read",
					"update"
				],
				"rgw": [
					"create",
					"delete",
					"read",
					"update"
				],
				"user": [
					"create",
					"delete",
					"read",
					"update"
				]
			},
			"system": true
		},
		{
			"name": "block-manager",
			"description": "allows full permissions for rbd-image, rbd-mirroring, and iscsi scopes",
			"scopes_permissions": {
				"rbd-image": [
					"read",
					"create",
					"update",
					"delete"
				],
				"pool": [
					"read"
				],
				"iscsi": [
					"read",
					"create",
					"update",
					"delete"
				],
				"rbd-mirroring": [
					"read",
					"create",
					"update",
					"delete"
				],
				"grafana": [
					"read"
				],
				"nvme-of": [
					"read",
					"create",
					"update",
					"delete"
				]
			},
			"system": true
		},
		{
			"name": "cephfs-manager",
			"description": "allows full permissions for the cephfs scope",
			"scopes_permissions": {
				"cephfs": [
					"read",
					"create",
					"update",
					"delete"
				],
				"grafana": [
					"read"
				]
			},
			"system": true
		},
		{
			"name": "cluster-manager",
			"description": "allows full permissions for the hosts, osd, mon, mgr,\n    and config-opt scopes",
			"scopes_permissions": {
				"hosts": [
					"read",
					"create",
					"update",
					"delete"
				],
				"osd": [
					"read",
					"create",
					"update",
					"delete"
				],
				"monitor": [
					"read",
					"create",
					"update",
					"delete"
				],
				"manager": [
					"read",
					"create",
					"update",
					"delete"
				],
				"config-opt": [
					"read",
					"create",
					"update",
					"delete"
				],
				"log": [
					"read",
					"create",
					"update",
					"delete"
				],
				"grafana": [
					"read"
				],
				"prometheus": [
					"read"
				]
			},
			"system": true
		},
		{
			"name": "ganesha-manager",
			"description": "allows full permissions for the nfs-ganesha scope",
			"scopes_permissions": {
				"nfs-ganesha": [
					"read",
					"create",
					"update",
					"delete"
				],
				"cephfs": [
					"read",
					"create",
					"update",
					"delete"
				],
				"rgw": [
					"read",
					"create",
					"update",
					"delete"
				],
				"grafana": [
					"read"
				]
			},
			"system": true
		},
		{
			"name": "pool-manager",
			"description": "allows full permissions for the pool scope",
			"scopes_permissions": {
				"pool": [
					"read",
					"create",
					"update",
					"delete"
				],
				"grafana": [
					"read"
				]
			},
			"system": true
		},
		{
			"name": "read-only",
			"description": "allows read permission for all security scope except dashboard settings and config-opt",
			"scopes_permissions": {
				"cephfs": [
					"read"
				],
				"grafana": [
					"read"
				],
				"hosts": [
					"read"
				],
				"iscsi": [
					"read"
				],
				"log": [
					"read"
				],
				"manager": [
					"read"
				],
				"monitor": [
					"read"
				],
				"nfs-ganesha": [
					"read"
				],
				"nvme-of": [
					"read"
				],
				"osd": [
					"read"
				],
				"pool": [
					"read"
				],
				"prometheus": [
					"read"
				],
				"rbd-image": [
					"read"
				],
				"rbd-mirroring": [
					"read"
				],
				"rgw": [
					"read"
				],
				"user": [
					"read"
				]
			},
			"system": true
		},
		{
			"name": "rgw-manager",
			"description": "allows full permissions for the rgw scope",
			"scopes_permissions": {
				"rgw": [
					"read",
					"create",
					"update",
					"delete"
				],
				"grafana": [
					"read"
				]
			},
			"system": true
		}
	]`
)
