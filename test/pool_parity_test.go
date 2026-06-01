//go:build cgo

package test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/clyso/ceph-api/test/parity"
	"github.com/stretchr/testify/require"
)

const poolWriteAccept = "application/vnd.ceph.api.v1.0+json"

func Test_Parity_Pool_Create(t *testing.T) {
	r := parity.New(t)

	const name = "parity-pool-create"
	// Both backends drive the same Ceph cluster (the dashboard is its mgr
	// module), so pool deletion happens once via the CLI rather than a
	// per-backend HTTP delete (pool delete isn't a ported route).
	delPool := func() {
		cephEnv.Exec(context.Background(), []string{
			"ceph", "osd", "pool", "delete", name, name, "--yes-i-really-really-mean-it",
		})
	}
	t.Cleanup(delPool)

	// The flat body shape the dashboard create dialog actually sends.
	createBody := map[string]any{
		"pool":                 name,
		"pg_num":               8,
		"pool_type":            "replicated",
		"rule_name":            "replicated_rule",
		"application_metadata": []string{"rbd"},
		"pg_autoscale_mode":    "on",
		"size":                 2,
	}
	create := parity.Call{Method: "POST", Path: "/api/pool", Body: createBody, Accept: poolWriteAccept}

	// A 2xx alone doesn't prove the kwargs landed (a silently dropped key would
	// still create the pool at defaults). After each backend creates, assert the
	// settings the body exercised via CLI so a divergence in the mon command
	// sequence surfaces on both backends. The dashboard runs create as an async
	// task, so poll for the settings to settle before asserting.
	assertSettings := func(b parity.Backend) {
		settled := func() bool {
			out, err := cephEnv.Exec(context.Background(), []string{"ceph", "osd", "pool", "get", name, "size", "-f", "json"})
			return err == nil && strings.Contains(out, `"size":2`)
		}
		deadline := time.Now().Add(30 * time.Second)
		for !settled() && time.Now().Before(deadline) {
			time.Sleep(time.Second)
		}

		out, err := cephEnv.Exec(context.Background(), []string{"ceph", "osd", "pool", "get", name, "size", "-f", "json"})
		require.NoError(t, err, "%s: read back size", b)
		require.Contains(t, out, `"size":2`, "%s: size kwarg not applied", b)

		out, err = cephEnv.Exec(context.Background(), []string{"ceph", "osd", "pool", "get", name, "pg_autoscale_mode", "-f", "json"})
		require.NoError(t, err, "%s: read back pg_autoscale_mode", b)
		require.Contains(t, out, `"pg_autoscale_mode":"on"`, "%s: pg_autoscale_mode kwarg not applied", b)

		out, err = cephEnv.Exec(context.Background(), []string{"ceph", "osd", "pool", "application", "get", name, "-f", "json"})
		require.NoError(t, err, "%s: read back application", b)
		require.Contains(t, out, "rbd", "%s: application_metadata not applied", b)
	}

	for _, b := range r.Backends(create) {
		delPool()
		resp, _ := r.DoRecord(b, create)
		require.True(t, resp.StatusCode/100 == 2, "%s: create pool: status %d", b, resp.StatusCode)
		assertSettings(b)
	}
}
