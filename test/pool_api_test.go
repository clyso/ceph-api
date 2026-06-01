//go:build cgo

package test

import (
	"context"
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// findPool returns the pool object with pool_name == name from a ListPools
// response, or nil if absent.
func findPool(pools []*structpb.Struct, name string) map[string]any {
	for _, p := range pools {
		m := p.AsMap()
		if m["pool_name"] == name {
			return m
		}
	}
	return nil
}

func Test_Pool_Create(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	const name = "e2e-pool-create"
	cleanup := func() {
		// rados-free CLI delete; the pool DELETE endpoint is not ported.
		_, _ = cephEnv.Exec(context.Background(), []string{
			"ceph", "osd", "pool", "delete", name, name,
			"--yes-i-really-really-mean-it",
		})
	}
	cleanup()
	t.Cleanup(cleanup)

	t.Run("list before create omits the pool", func(t *testing.T) {
		res, err := client.ListPools(tstCtx, &pb.ListPoolsRequest{})
		r.NoError(err)
		r.Nil(findPool(res.Pools, name))
	})

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
	})

	t.Run("list after create returns the pool with serialized fields", func(t *testing.T) {
		res, err := client.ListPools(tstCtx, &pb.ListPoolsRequest{})
		r.NoError(err)
		pool := findPool(res.Pools, name)
		r.NotNil(pool, "created pool must appear in the list")

		// _serialize_pool transforms: type int→string, crush_rule id→name,
		// application_metadata dict→list-of-keys.
		r.Equal("replicated", pool["type"])
		r.Equal("replicated_rule", pool["crush_rule"])
		r.Equal([]any{"rbd"}, pool["application_metadata"])

		// fields the create set, read back through the ported LIST.
		r.Equal("on", pool["pg_autoscale_mode"])
		r.EqualValues(1073741824, pool["quota_max_bytes"])
		r.EqualValues(1000, pool["quota_max_objects"])

		// read_balance is a mixed-type object preserved as-is.
		rb, ok := pool["read_balance"].(map[string]any)
		r.True(ok, "read_balance must be an object")
		r.NotEmpty(rb["score_type"])
	})

	t.Run("attrs is accepted (response not field-filtered)", func(t *testing.T) {
		// attrs cannot be honored under a typed Struct response (documented
		// divergence); the param is accepted and the full object returned.
		res, err := client.ListPools(tstCtx, &pb.ListPoolsRequest{Attrs: "pool_name,size"})
		r.NoError(err)
		pool := findPool(res.Pools, name)
		r.NotNil(pool)
		r.Equal("replicated", pool["type"], "full attribute set still returned")
	})

	t.Run("stats=true is not supported", func(t *testing.T) {
		_, err := client.ListPools(tstCtx, &pb.ListPoolsRequest{Stats: true})
		r.Error(err)
		r.Contains(err.Error(), "NotImplemented")
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
