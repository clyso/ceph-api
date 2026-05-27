//go:build cgo

package test

import (
	"context"
	"net/http"
	"testing"

	"github.com/clyso/ceph-api/test/parity"
	"github.com/clyso/ceph-api/test/testenv"
	"github.com/stretchr/testify/require"
)

const userAccept = "application/vnd.ceph.api.v1.0+json"
const roleAccept = "application/vnd.ceph.api.v1.0+json"

func Test_Parity_User_List(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/user", Accept: userAccept}
	for _, b := range r.Backends(call) {
		resp, _ := r.DoRecord(b, call)
		require.True(t, resp.StatusCode/100 == 2, "%s: list users: status %d", b, resp.StatusCode)
	}
}

func Test_Parity_User_Get(t *testing.T) {
	r := parity.New(t)
	get := parity.Call{
		Method: "GET", Path: "/api/user/{username}",
		PathParams: map[string]string{"username": testenv.DashboardUser},
		Accept:     userAccept,
	}
	for _, b := range r.Backends(get) {
		resp, _ := r.DoRecord(b, get)
		require.True(t, resp.StatusCode/100 == 2, "%s: get user: status %d", b, resp.StatusCode)
	}
}

func Test_Parity_User_CRUD(t *testing.T) {
	r := parity.New(t)

	const username = "parity-user-crud"
	createBody := map[string]any{
		"username":          username,
		"password":          "parity-user-crud-pass",
		"name":              "parity user crud",
		"email":             "",
		"roles":             []string{"administrator"},
		"enabled":           true,
		"pwdUpdateRequired": true,
	}
	updateBody := map[string]any{
		"name":    "parity user crud updated",
		"email":   "parity@example.com",
		"enabled": true,
	}
	changePassBody := map[string]any{
		"old_password": "parity-user-crud-pass",
		"new_password": "parity-user-crud-pass-2",
	}
	pp := map[string]string{"username": username}

	create := parity.Call{Method: "POST", Path: "/api/user", Body: createBody, Accept: userAccept}
	update := parity.Call{Method: "PUT", Path: "/api/user/{username}", PathParams: pp, Body: updateBody, Accept: userAccept}
	changePass := parity.Call{
		Method: "POST", Path: "/api/user/{username}/change_password",
		PathParams: pp, Body: changePassBody, Accept: userAccept,
	}
	del := parity.Call{Method: "DELETE", Path: "/api/user/{username}", PathParams: pp, Accept: userAccept}

	r.Do(parity.Ours, del)
	t.Cleanup(func() { r.Do(parity.Ours, del) })

	for _, b := range r.Backends(create) {
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusConflict,
			"%s: create user: status %d", b, resp.StatusCode)
		resp, _ = r.DoRecord(b, update)
		require.True(t, resp.StatusCode/100 == 2, "%s: update user: status %d", b, resp.StatusCode)

		// Dashboard's change_password rejects calls where the JWT subject
		// differs from {username}, so log in as the created user first and
		// drive the call from that identity on both backends.
		backend := parity.ClientFor(b)
		userClient, err := parity.Login(context.Background(), backend.BaseURL, backend.HTTP,
			userAccept, username, "parity-user-crud-pass")
		require.NoError(t, err, "%s: login as %s", b, username)
		resp, _ = r.DoRecordAs(b, changePass, userClient)
		require.True(t, resp.StatusCode/100 == 2, "%s: change_password: status %d", b, resp.StatusCode)

		resp, _ = r.DoRecord(b, del)
		require.True(t, resp.StatusCode/100 == 2,
			"%s: delete user: status %d", b, resp.StatusCode)
	}
}

func Test_Parity_Role_List(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/role", Accept: roleAccept}
	for _, b := range r.Backends(call) {
		resp, _ := r.DoRecord(b, call)
		require.True(t, resp.StatusCode/100 == 2, "%s: list roles: status %d", b, resp.StatusCode)
	}
}

func Test_Parity_Role_Get(t *testing.T) {
	r := parity.New(t)
	get := parity.Call{
		Method: "GET", Path: "/api/role/{name}",
		PathParams: map[string]string{"name": "administrator"},
		Accept:     roleAccept,
	}
	for _, b := range r.Backends(get) {
		resp, _ := r.DoRecord(b, get)
		require.True(t, resp.StatusCode/100 == 2, "%s: get role: status %d", b, resp.StatusCode)
	}
}

func Test_Parity_Role_CRUD(t *testing.T) {
	r := parity.New(t)

	const name = "parity-role"
	createBody := map[string]any{
		"name":               name,
		"description":        "parity test role",
		"scopes_permissions": map[string][]string{"hosts": {"read"}},
	}
	updateBody := map[string]any{
		"description":        "parity test role updated",
		"scopes_permissions": map[string][]string{"hosts": {"read", "create"}},
	}
	rolePP := map[string]string{"name": name}

	create := parity.Call{Method: "POST", Path: "/api/role", Body: createBody, Accept: roleAccept}
	update := parity.Call{Method: "PUT", Path: "/api/role/{name}", PathParams: rolePP, Body: updateBody, Accept: roleAccept}
	delRole := parity.Call{Method: "DELETE", Path: "/api/role/{name}", PathParams: rolePP, Accept: roleAccept}

	r.Do(parity.Ours, delRole)
	t.Cleanup(func() { r.Do(parity.Ours, delRole) })

	for _, b := range r.Backends(create) {
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusConflict,
			"%s: create role: status %d", b, resp.StatusCode)
		resp, _ = r.DoRecord(b, update)
		require.True(t, resp.StatusCode/100 == 2, "%s: update role: status %d", b, resp.StatusCode)
		resp, _ = r.DoRecord(b, delRole)
		require.True(t, resp.StatusCode/100 == 2,
			"%s: delete role: status %d", b, resp.StatusCode)
	}
}

// Separate from Role_CRUD because ceph-api's clone is GET
// /api/user/{name}/clone?new_name= while the dashboard's is POST
// /api/role/{name}/clone with new_name in the body — different
// method + path shape, so r.Backends() collapses to [Ours] and
// there's nothing to compare against.
func Test_Parity_Role_Clone(t *testing.T) {
	r := parity.New(t)

	const name = "parity-role-clone-src"
	const cloneName = name + "-clone"
	createBody := map[string]any{
		"name":               name,
		"description":        "parity clone src role",
		"scopes_permissions": map[string][]string{"hosts": {"read"}},
	}
	srcPP := map[string]string{"name": name}
	clonePP := map[string]string{"name": cloneName}
	create := parity.Call{Method: "POST", Path: "/api/role", Body: createBody, Accept: roleAccept}
	clone := parity.Call{
		Method: "GET", Path: "/api/user/{name}/clone",
		PathParams:  srcPP,
		QueryParams: map[string]string{"new_name": cloneName},
		Accept:      roleAccept,
	}
	delSrc := parity.Call{Method: "DELETE", Path: "/api/role/{name}", PathParams: srcPP, Accept: roleAccept}
	delClone := parity.Call{Method: "DELETE", Path: "/api/role/{name}", PathParams: clonePP, Accept: roleAccept}

	r.Do(parity.Ours, delClone)
	r.Do(parity.Ours, delSrc)
	t.Cleanup(func() {
		r.Do(parity.Ours, delClone)
		r.Do(parity.Ours, delSrc)
	})

	resp, _ := r.Do(parity.Ours, create)
	require.True(t, resp.StatusCode/100 == 2, "create role: status %d", resp.StatusCode)
	for _, b := range r.Backends(clone) {
		resp, _ := r.DoRecord(b, clone)
		require.True(t, resp.StatusCode/100 == 2, "%s: clone role: status %d", b, resp.StatusCode)
	}
}
