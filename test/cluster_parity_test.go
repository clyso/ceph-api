//go:build cgo

package test

import (
	"net/http"
	"testing"

	"github.com/clyso/ceph-api/test/parity"
	"github.com/stretchr/testify/require"
)

// /api/cluster uses v0.1; /api/cluster/user* uses v1.0. Dashboard
// returns 415 if the Accept does not match the per-route version.
const (
	clusterStatusAccept = "application/vnd.ceph.api.v0.1+json"
	clusterUserAccept   = "application/vnd.ceph.api.v1.0+json"
)

func Test_Parity_Cluster_Status(t *testing.T) {
	r := parity.New(t)

	get := parity.Call{Method: "GET", Path: "/api/cluster", Accept: clusterStatusAccept}
	put := parity.Call{
		Method: "PUT", Path: "/api/cluster",
		Body:   map[string]string{"status": "POST_INSTALLED"},
		Accept: clusterStatusAccept,
	}

	for _, b := range r.Backends(get) {
		r.DoRecord(b, get)
	}
	for _, b := range r.Backends(put) {
		r.DoRecord(b, put)
	}

	t.Cleanup(func() {
		r.Do(parity.Ours, parity.Call{
			Method: "PUT", Path: "/api/cluster",
			Body:   map[string]string{"status": "INSTALLED"},
			Accept: clusterStatusAccept,
		})
	})
}

func Test_Parity_Cluster_ConfigSearch(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/cluster/config/search", Accept: clusterStatusAccept}
	for _, b := range r.Backends(call) {
		r.DoRecord(b, call)
	}
}

func Test_Parity_Cluster_Users_CRUD(t *testing.T) {
	r := parity.New(t)

	const entity = "client.parity-cluster-user"
	createBody := map[string]any{
		"user_entity":  entity,
		"capabilities": []map[string]string{{"entity": "mon", "cap": "allow r"}},
	}
	updateBody := map[string]any{
		"user_entity":  entity,
		"capabilities": []map[string]string{{"entity": "mon", "cap": "allow rw"}},
	}
	exportBody := map[string]any{"entities": []string{entity}}

	list := parity.Call{Method: "GET", Path: "/api/cluster/user", Accept: clusterUserAccept}
	create := parity.Call{Method: "POST", Path: "/api/cluster/user", Body: createBody, Accept: clusterUserAccept}
	update := parity.Call{Method: "PUT", Path: "/api/cluster/user", Body: updateBody, Accept: clusterUserAccept}
	export := parity.Call{Method: "POST", Path: "/api/cluster/user/export", Body: exportBody, Accept: clusterUserAccept}
	del := parity.Call{
		Method: "DELETE", Path: "/api/cluster/user/{user_entity}",
		PathParams: map[string]string{"user_entity": entity},
		Accept:     clusterUserAccept,
	}

	r.Do(parity.Ours, del)
	t.Cleanup(func() { r.Do(parity.Ours, del) })

	for _, b := range r.Backends(list) {
		r.DoRecord(b, list)
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusConflict,
			"%s: create cluster user: status %d", b, resp.StatusCode)
		r.DoRecord(b, update)
		r.DoRecord(b, export)
		resp, _ = r.DoRecord(b, del)
		require.True(t, resp.StatusCode/100 == 2,
			"%s: delete cluster user: status %d", b, resp.StatusCode)
	}
}
