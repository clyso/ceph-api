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
		resp, _ := r.DoRecord(b, get)
		require.True(t, resp.StatusCode/100 == 2, "%s: get cluster status: status %d", b, resp.StatusCode)
	}
	for _, b := range r.Backends(put) {
		resp, _ := r.DoRecord(b, put)
		require.True(t, resp.StatusCode/100 == 2, "%s: put cluster status: status %d", b, resp.StatusCode)
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
		resp, _ := r.DoRecord(b, call)
		require.True(t, resp.StatusCode/100 == 2, "%s: config search: status %d", b, resp.StatusCode)
	}
}

func Test_Parity_Cluster_Users_CRUD(t *testing.T) {
	r := parity.New(t)

	const entity = "client.parity-cluster-user"
	// Fixed cephx key so both backends' `auth import` results are byte-identical
	// and the export bodies match. `auth add` would generate a fresh random key
	// per backend, leaving the export pair diverging on the key value alone.
	const importData = "[client.parity-cluster-user]\n" +
		"\tkey = AQDe6jdmAAAAABAAyVQa6dJoHpzaTBLcQjQGOQ==\n" +
		"\tcaps mon = \"allow r\"\n"
	createBody := map[string]any{
		"import_data": importData,
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
		resp, _ := r.DoRecord(b, list)
		require.True(t, resp.StatusCode/100 == 2, "%s: list cluster users: status %d", b, resp.StatusCode)
		resp, _ = r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusConflict,
			"%s: create cluster user: status %d", b, resp.StatusCode)
		resp, _ = r.DoRecord(b, update)
		require.True(t, resp.StatusCode/100 == 2, "%s: update cluster user: status %d", b, resp.StatusCode)
		resp, _ = r.DoRecord(b, export)
		require.True(t, resp.StatusCode/100 == 2, "%s: export cluster user: status %d", b, resp.StatusCode)
		resp, _ = r.DoRecord(b, del)
		require.True(t, resp.StatusCode/100 == 2,
			"%s: delete cluster user: status %d", b, resp.StatusCode)
	}
}
