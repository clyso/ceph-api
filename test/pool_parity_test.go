//go:build cgo

package test

import (
	"testing"

	"github.com/clyso/ceph-api/test/parity"
	"github.com/stretchr/testify/require"
)

const poolWriteAccept = "application/vnd.ceph.api.v1.0+json"

// The body sends every modeled follow-up (application_metadata, quotas,
// compression_mode, pg_autoscale_mode) so the create + 0..N mon command
// sequence is exercised on both backends. ceph-api models only the named
// fields plus pg_autoscale_mode/compression_mode; the dashboard's generic
// **kwargs passthrough (arbitrary "osd pool set var") is intentionally not
// ported for v1 — see tasks/post-api-pool.md §Request body. The dashboard
// wraps create in an async task, so it answers 201/null or 202/task-envelope
// depending on whether the work finishes within its 2s wait; ceph-api is
// synchronous (always 201/empty). That status+body divergence is declared in
// test/parity/api_diff.yaml ("POST /api/pool").
func Test_Parity_Pool_Create(t *testing.T) {
	r := parity.New(t)

	const name = "parity-pool"
	createBody := map[string]any{
		"pool":                 name,
		"pg_num":               8,
		"pool_type":            "replicated",
		"rule_name":            "replicated_rule",
		"application_metadata": []string{"rbd"},
		"pg_autoscale_mode":    "on",
		"compression_mode":     "aggressive",
		"quota_max_bytes":      1073741824,
		"quota_max_objects":    1000,
	}
	create := parity.Call{Method: "POST", Path: "/api/pool", Body: createBody, Accept: poolWriteAccept}

	// osd pool create is idempotent at the mon, so both backends creating
	// the same pool name on the shared cluster each return 2xx.
	for _, b := range r.Backends(create) {
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2, "%s: create pool: status %d", b, resp.StatusCode)
	}
}
