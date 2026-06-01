//go:build cgo

package test

import (
	"context"
	"testing"

	"github.com/clyso/ceph-api/test/parity"
	"github.com/stretchr/testify/require"
)

// Both the list and create routes use v1.0; dashboard returns 415 otherwise.
const poolAccept = "application/vnd.ceph.api.v1.0+json"

func Test_Parity_Pool_List(t *testing.T) {
	r := parity.New(t)
	// Default request (no attrs filter, stats omitted) so every pool returns
	// its fullest serialized body across both backends.
	call := parity.Call{Method: "GET", Path: "/api/pool", Accept: poolAccept}
	for _, b := range r.Backends(call) {
		resp, _ := r.DoRecord(b, call)
		require.True(t, resp.StatusCode/100 == 2, "%s: list pools: status %d", b, resp.StatusCode)
	}
}

func Test_Parity_Pool_Create(t *testing.T) {
	r := parity.New(t)

	const name = "parity-pool-create"
	// Flat dashboard body: named fields plus open **kwargs (pg_autoscale_mode)
	// sent as top-level keys, exactly as the dashboard create dialog sends them.
	createBody := map[string]any{
		"pool":                 name,
		"pg_num":               8,
		"pool_type":            "replicated",
		"rule_name":            "replicated_rule",
		"application_metadata": []any{"rbd"},
		"pg_autoscale_mode":    "on",
		"quota_max_bytes":      1073741824,
		"quota_max_objects":    1000,
	}
	create := parity.Call{Method: "POST", Path: "/api/pool", Body: createBody, Accept: poolAccept}

	del := func() {
		_, _ = cephEnv.Exec(context.Background(), []string{
			"ceph", "osd", "pool", "delete", name, name,
			"--yes-i-really-really-mean-it",
		})
	}
	del()
	t.Cleanup(del)

	for _, b := range r.Backends(create) {
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2, "%s: create pool: status %d", b, resp.StatusCode)
		// Each backend leaves the pool behind; remove it before the next pass
		// so the second create is not an already-exists.
		del()
	}
}
