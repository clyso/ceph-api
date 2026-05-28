package user

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/clyso/ceph-api/pkg/types"
)

// NormalizeInlineScopes validates and canonicalizes API-key inline scopes.
// Accepted forms are "scope:permission" and "scope:permission/permission".
func NormalizeInlineScopes(in []string) ([]string, error) {
	permissions, err := PermissionsFromInlineScopes(in)
	if err != nil {
		return nil, err
	}

	res := make([]string, 0, len(in))
	for scope, perms := range permissions {
		for _, perm := range perms {
			res = append(res, scope+":"+perm)
		}
	}
	sort.Strings(res)
	return res, nil
}

func PermissionsFromInlineScopes(in []string) (map[string][]string, error) {
	res := map[string][]string{}
	for _, rawScope := range in {
		scope, rawPerms, ok := strings.Cut(strings.TrimSpace(rawScope), ":")
		if !ok || scope == "" || rawPerms == "" {
			return nil, fmt.Errorf("%w: invalid scope %q, expected scope:permission", types.ErrInvalidArg, rawScope)
		}
		if _, ok := scopeSet[scope]; !ok {
			return nil, fmt.Errorf("%w: unknown scope %q", types.ErrInvalidArg, scope)
		}

		for _, rawPerm := range strings.Split(rawPerms, "/") {
			perm := strings.TrimSpace(rawPerm)
			if perm == "" {
				return nil, fmt.Errorf("%w: invalid empty permission in scope %q", types.ErrInvalidArg, rawScope)
			}
			if _, ok := permissionSet[perm]; !ok {
				return nil, fmt.Errorf("%w: unknown permission %q", types.ErrInvalidArg, perm)
			}
			if !slices.Contains(res[scope], perm) {
				res[scope] = append(res[scope], perm)
			}
		}
	}

	for scope := range res {
		sort.Strings(res[scope])
	}
	return res, nil
}
