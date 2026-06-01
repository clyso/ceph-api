//go:build cgo

package test

import (
	"context"
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func Test_Pool_Create(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	const name = "e2e-pool-create"
	cleanup := func() {
		// rados-free CLI delete; the pool GET/DELETE endpoints are not ported.
		_, _ = cephEnv.Exec(context.Background(), []string{
			"ceph", "osd", "pool", "delete", name, name,
			"--yes-i-really-really-mean-it",
		})
	}
	cleanup()
	t.Cleanup(cleanup)

	t.Run("happy path with quotas, app, and a kwarg", func(t *testing.T) {
		body, err := structpb.NewStruct(map[string]any{
			"pool":                 name,
			"pg_num":               8,
			"pool_type":            "replicated",
			"rule_name":            "replicated_rule",
			"application_metadata": []any{"rbd"},
			"pg_autoscale_mode":    "on",
			"quota_max_bytes":      1073741824,
			"quota_max_objects":    1000,
		})
		r.NoError(err)
		_, err = client.CreatePool(tstCtx, body)
		r.NoError(err)

		// TODO(crud-readback): replace with Pool Get once ported.
		out, err := cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "get", name, "pg_autoscale_mode", "-f", "json"})
		r.NoError(err)
		r.Contains(out, "\"pg_autoscale_mode\":\"on\"")

		// TODO(crud-readback): replace with Pool Get once ported.
		quota, err := cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "get-quota", name, "-f", "json"})
		r.NoError(err)
		r.Contains(quota, "1073741824")
		r.Contains(quota, "1000")

		// TODO(crud-readback): replace with Pool Get once ported.
		app, err := cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "application", "get", name, "-f", "json"})
		r.NoError(err)
		r.Contains(app, "rbd")
	})

	t.Run("re-create existing pool is idempotent", func(t *testing.T) {
		body, err := structpb.NewStruct(map[string]any{
			"pool":      name,
			"pg_num":    8,
			"pool_type": "replicated",
		})
		r.NoError(err)
		// `osd pool create` returns success when the pool already exists.
		_, err = client.CreatePool(tstCtx, body)
		r.NoError(err)
	})

	t.Run("missing required field pool", func(t *testing.T) {
		body, err := structpb.NewStruct(map[string]any{
			"pg_num":    8,
			"pool_type": "replicated",
		})
		r.NoError(err)
		_, err = client.CreatePool(tstCtx, body)
		r.Error(err)
		r.Contains(err.Error(), "InvalidArgument")
	})

	t.Run("invalid pool_type enum", func(t *testing.T) {
		body, err := structpb.NewStruct(map[string]any{
			"pool":      "e2e-pool-badtype",
			"pg_num":    8,
			"pool_type": "bogus",
		})
		r.NoError(err)
		_, err = client.CreatePool(tstCtx, body)
		r.Error(err)
		r.Contains(err.Error(), "InvalidArgument")
	})

	t.Run("ec_overwrites on replicated pool is rejected by mon", func(t *testing.T) {
		body, err := structpb.NewStruct(map[string]any{
			"pool":      "e2e-pool-ecflag",
			"pg_num":    8,
			"pool_type": "replicated",
			"flags":     []any{"ec_overwrites"},
		})
		r.NoError(err)
		t.Cleanup(func() {
			_, _ = cephEnv.Exec(context.Background(), []string{
				"ceph", "osd", "pool", "delete", "e2e-pool-ecflag", "e2e-pool-ecflag",
				"--yes-i-really-really-mean-it",
			})
		})
		// The create succeeds; the follow-up `osd pool set allow_ec_overwrites`
		// fails because the pool is replicated. The mon -EINVAL must surface as
		// InvalidArgument (400), not a blanket Internal (500).
		_, err = client.CreatePool(tstCtx, body)
		r.Error(err)
		r.Contains(err.Error(), "InvalidArgument")
	})
}
