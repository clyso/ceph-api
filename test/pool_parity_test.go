//go:build cgo

package test

import (
	"testing"

	"github.com/clyso/ceph-api/test/parity"
	"github.com/stretchr/testify/require"
)

const poolWriteAccept = "application/vnd.ceph.api.v1.0+json"

// There is no DELETE /api/pool endpoint yet, so unlike the crush_rule parity
// test we cannot pre-clean or t.Cleanup the pool. This is safe because
// `osd pool create` is idempotent on both backends: re-running over a
// pre-existing pool still returns a 2xx, so the test is robust across repeated
// runs. The response body is ignored via api_diff.yaml (POST /api/pool, path
// $), so only the status class is compared; these tests assert that both
// backends accept the request and that the various optional fields (quota,
// options, erasure + ec_overwrites) drive the create without error.

func Test_Parity_Pool_Create(t *testing.T) {
	r := parity.New(t)

	const name = "parity_pool_create"
	createBody := map[string]any{
		"pool":                 name,
		"pg_num":               8,
		"pool_type":            "replicated",
		"rule_name":            "replicated_rule",
		"application_metadata": []string{"rbd"},
		"size":                 2,
		"pg_autoscale_mode":    "on",
		"compression_mode":     "none",
		"quota_max_bytes":      1073741824,
		"quota_max_objects":    1000,
	}
	create := parity.Call{Method: "POST", Path: "/api/pool", Body: createBody, Accept: poolWriteAccept}

	for _, b := range r.Backends(create) {
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2, "%s: create pool: status %d", b, resp.StatusCode)
	}
}

func Test_Parity_Pool_Create_Erasure(t *testing.T) {
	r := parity.New(t)

	const name = "parity_pool_create_ec"
	createBody := map[string]any{
		"pool":                 name,
		"pg_num":               8,
		"pool_type":            "erasure",
		"erasure_code_profile": "default",
		"flags":                "ec_overwrites",
		"application_metadata": []string{"rbd"},
	}
	create := parity.Call{Method: "POST", Path: "/api/pool", Body: createBody, Accept: poolWriteAccept}

	for _, b := range r.Backends(create) {
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2, "%s: create erasure pool: status %d", b, resp.StatusCode)
	}
}
