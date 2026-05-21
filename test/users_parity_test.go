//go:build cgo

package test

import (
	"net/http"
	"testing"

	"github.com/clyso/ceph-api/test/parity"
	"github.com/stretchr/testify/require"
)

const userAccept = "application/vnd.ceph.api.v1.0+json"
const roleAccept = "application/vnd.ceph.api.v1.0+json"

func Test_Parity_User_List(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/user", Accept: userAccept}
	for _, b := range r.Backends(call) {
		r.DoRecord(b, call)
	}
}

func Test_Parity_User_Get(t *testing.T) {
	r := parity.New(t)
	get := parity.Call{
		Method: "GET", Path: "/api/user/{username}",
		PathParams: map[string]string{"username": admin}, Accept: userAccept,
	}
	for _, b := range r.Backends(get) {
		r.DoRecord(b, get)
	}
}

func Test_Parity_User_CRUD(t *testing.T) {
	r := parity.New(t)

	const username = "parity-user-crud"
	createBody := map[string]any{
		"username":            username,
		"password":            "parity-user-crud-pass",
		"name":                "parity user crud",
		"email":               "",
		"roles":               []string{"administrator"},
		"enabled":             true,
		"pwd_expiration_date": nil,
		"pwd_update_required": false,
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
		r.DoRecord(b, update)
		r.DoRecord(b, changePass)
		resp, _ = r.DoRecord(b, del)
		require.True(t, resp.StatusCode/100 == 2,
			"%s: delete user: status %d", b, resp.StatusCode)
	}
}

func Test_Parity_Role_List(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/role", Accept: roleAccept}
	for _, b := range r.Backends(call) {
		r.DoRecord(b, call)
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
		r.DoRecord(b, get)
	}
}

func Test_Parity_Role_CRUD(t *testing.T) {
	r := parity.New(t)

	const name = "parity-role"
	const cloneName = name + "-clone"
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
	clonePP := map[string]string{"name": cloneName}

	create := parity.Call{Method: "POST", Path: "/api/role", Body: createBody, Accept: roleAccept}
	update := parity.Call{Method: "PUT", Path: "/api/role/{name}", PathParams: rolePP, Body: updateBody, Accept: roleAccept}
	clone := parity.Call{
		Method: "GET", Path: "/api/user/{name}/clone",
		PathParams:  rolePP,
		QueryParams: map[string]string{"new_name": cloneName},
		Accept:      roleAccept,
	}
	delRole := parity.Call{Method: "DELETE", Path: "/api/role/{name}", PathParams: rolePP, Accept: roleAccept}
	delClone := parity.Call{Method: "DELETE", Path: "/api/role/{name}", PathParams: clonePP, Accept: roleAccept}

	r.Do(parity.Ours, delClone)
	r.Do(parity.Ours, delRole)
	t.Cleanup(func() {
		r.Do(parity.Ours, delClone)
		r.Do(parity.Ours, delRole)
	})

	for _, b := range r.Backends(create) {
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusConflict,
			"%s: create role: status %d", b, resp.StatusCode)
		r.DoRecord(b, update)
		r.DoRecord(b, clone)
		r.Do(b, delClone)
		resp, _ = r.DoRecord(b, delRole)
		require.True(t, resp.StatusCode/100 == 2,
			"%s: delete role: status %d", b, resp.StatusCode)
	}
}
