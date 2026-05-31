//go:build cgo

package test

import (
	"testing"

	"github.com/clyso/ceph-api/test/parity"
	"github.com/stretchr/testify/require"
)

const poolAccept = "application/vnd.ceph.api.v1.0+json"

// GET /api/pool is exercised on the default (stats=false) path: ceph-api
// reconstructs the bare pool array from `osd dump` + `osd crush dump`,
// reproducing the dashboard's three serialize transforms (type int->name,
// crush_rule id->name, application_metadata dict->key list).
//
// The stats=true path is NOT exercised here: its `stats[*].rate`/`rates` and
// `pg_status` come from the mgr's in-process counter/pg caches, which have no
// mon-command equivalent, so those fields cannot be faithfully reproduced. The
// attrs= field-whitelist is likewise not reproducible with a typed proto
// response (which always emits all populated fields); ceph-api returns the
// full object regardless of attrs.
func Test_Parity_Pool_List(t *testing.T) {
	r := parity.New(t)

	call := parity.Call{Method: "GET", Path: "/api/pool", Accept: poolAccept}
	for _, b := range r.Backends(call) {
		resp, _ := r.DoRecord(b, call)
		require.True(t, resp.StatusCode/100 == 2, "%s: list pools: status %d", b, resp.StatusCode)
	}
}

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
	create := parity.Call{Method: "POST", Path: "/api/pool", Body: createBody, Accept: poolAccept}

	// osd pool create is idempotent at the mon, so both backends creating
	// the same pool name on the shared cluster each return 2xx.
	for _, b := range r.Backends(create) {
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2, "%s: create pool: status %d", b, resp.StatusCode)
	}
}
