//go:build cgo

package test

import (
	"net/http"
	"testing"

	"github.com/clyso/ceph-api/test/parity"
	"github.com/stretchr/testify/require"
)

const crushRuleAccept = "application/vnd.ceph.api.v2.0+json"

func Test_Parity_CrushRule_List(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/crush_rule", Accept: crushRuleAccept}
	for _, b := range r.Backends(call) {
		r.DoRecord(b, call)
	}
}

func Test_Parity_CrushRule_Get(t *testing.T) {
	r := parity.New(t)
	get := parity.Call{
		Method: "GET", Path: "/api/crush_rule/{name}",
		PathParams: map[string]string{"name": "replicated_rule"},
		Accept:     crushRuleAccept,
	}
	for _, b := range r.Backends(get) {
		r.DoRecord(b, get)
	}
}

func Test_Parity_CrushRule_CRUD(t *testing.T) {
	r := parity.New(t)

	const name = "parity-crush-rule"
	createBody := map[string]any{
		"name":           name,
		"root":           "default",
		"failure_domain": "osd",
		"device_class":   "",
	}
	create := parity.Call{Method: "POST", Path: "/api/crush_rule", Body: createBody, Accept: crushRuleAccept}
	del := parity.Call{
		Method: "DELETE", Path: "/api/crush_rule/{name}",
		PathParams: map[string]string{"name": name}, Accept: crushRuleAccept,
	}

	r.Do(parity.Ours, del)
	t.Cleanup(func() { r.Do(parity.Ours, del) })

	for _, b := range r.Backends(create) {
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusConflict,
			"%s: create crush rule: status %d", b, resp.StatusCode)
		resp, _ = r.DoRecord(b, del)
		require.True(t, resp.StatusCode/100 == 2,
			"%s: delete crush rule: status %d", b, resp.StatusCode)
	}
}
