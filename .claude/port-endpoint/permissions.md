# Permission mapping: dashboard → Go handler

Every ported handler enforces authorization itself. There is **no central
permission table or per-rpc interceptor** — a missing check is a silently
unprotected endpoint. Confirm constants against current
`pkg/user/system_roles.go`; line numbers drift.

## The fixed scope/permission set

**Do not invent new scopes.** The set is closed and mirrors upstream
(`third_party/ceph/src/pybind/mgr/dashboard/security.py`).

Permission verbs (`pkg/user/system_roles.go`): `PermRead`, `PermCreate`,
`PermUpdate`, `PermDelete` → strings `read`/`create`/`update`/`delete`.

Scopes (string values, `system_roles.go` `scopeSet`): `hosts`,
`config-opt`, `pool`, `osd`, `monitor`, `rbd-image`, `iscsi`,
`rbd-mirroring`, `rgw`, `cephfs`, `manager`, `log`, `grafana`,
`prometheus`, `user`, `dashboard-settings`, `nfs-ganesha`, `nvme-of`.
Constants are `user.ScopeOsd`, `user.ScopePool`, etc.

## Enforcement mechanism

The auth interceptor (`pkg/auth/grpc_interceptor.go`) only
*authenticates* and loads the user's scope→perms map into the context
(`xctx.SetPermissions`). *Authorization* is the handler's job — as its
**first statement**:

```go
func (c *crushRuleAPI) CreateRule(ctx context.Context, req *pb.CreateRuleRequest) (*emptypb.Empty, error) {
    if err := user.HasPermissions(ctx, user.ScopeOsd, user.PermCreate); err != nil {
        return nil, err
    }
    ...
```

`user.HasPermissions(ctx, scope, perms...)` (`pkg/user/service.go`)
takes exactly **one scope** and one-or-more permissions (AND across the
perms), returns `types.ErrAccessDenied` on failure (which the
`ErrorInterceptor` maps to `PermissionDenied`). Empty permission map →
denied.

## Python → Go translation

A dashboard endpoint's required `(scope, permission)`:

- **Scope** = the `@APIRouter('/path', Scope.X)` (or `@UIRouter`) arg on
  the controller class. Map by **string value**, not enum name: Python
  `Scope.OSD == "osd"` → `user.ScopeOsd`.
- **Permission** = the method's decorator:

  | Python decorator | Go permission |
  |---|---|
  | `@ReadPermission` | `user.PermRead` |
  | `@CreatePermission` | `user.PermCreate` |
  | `@UpdatePermission` | `user.PermUpdate` |
  | `@DeletePermission` | `user.PermDelete` |

Emit, as the first handler lines:

```go
if err := user.HasPermissions(ctx, user.ScopeX, user.PermY); err != nil {
    return nil, err
}
```

### Default permission when a method has NO decorator

For `RESTController` subclasses, the standard methods (`list`, `get`,
`create`, `delete`, `set`/`singleton_set`/`update`,
`bulk_set`/`bulk_delete`) infer the permission from the HTTP verb when no
explicit `@*Permission` is present:
`GET→read`, `POST→create`, `PUT/PATCH→update`, `DELETE→delete`. An
explicit decorator always wins. A custom `@Endpoint` with no decorator
carries **no** permission via the verb fallback — read the source to
confirm the intended check; do not assume.

## Gotchas

- **Multi-scope:** `HasPermissions` enforces one scope per call. If an
  endpoint needs perms across two scopes, emit two separate calls (each
  `return nil, err`). Multiple perms in the *same* scope → pass them
  variadically: `user.HasPermissions(ctx, user.ScopeUser, user.PermRead, user.PermCreate)`.
- **`ScopeDashboardSettings` bug:** the constant is `"dashboard-setting"`
  (no trailing `s`) but the live `scopeSet` key and role JSON use
  `"dashboard-settings"`. Using the constant looks up the wrong key and
  never matches. For dashboard-settings endpoints, pass the literal
  `user.Scope("dashboard-settings")` and flag the bug in the task file.
- **No central binding:** the guard is hand-written in every handler
  method. Forgetting it = unprotected endpoint. The mechanical reviewer
  checks this first; the parity authz probes (`test/parity/probes.go`)
  also assert 401 (no token) and 403 (no perms) on every recorded
  endpoint, so a missing/over-broad guard fails parity.
