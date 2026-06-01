//go:build cgo

package test

import (
	"context"
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// findPool returns the pool element with the given pool_name from a ListPools
// response, or nil if absent.
func findPool(pools []*structpb.Struct, name string) *structpb.Struct {
	for _, p := range pools {
		if pn, ok := p.Fields["pool_name"]; ok && pn.GetStringValue() == name {
			return p
		}
	}
	return nil
}

func Test_Pool_Create(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	const name = "e2e-pool-create"
	t.Cleanup(func() {
		cephEnv.Exec(context.Background(), []string{
			"ceph", "osd", "pool", "delete", name, name, "--yes-i-really-really-mean-it",
		})
	})

	_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		Pool:                name,
		PgNum:               8,
		PoolType:            "replicated",
		RuleName:            proto.String("replicated_rule"),
		ApplicationMetadata: []string{"rbd"},
		PgAutoscaleMode:     proto.String("on"),
		Size:                proto.Int32(2),
		QuotaMaxObjects:     proto.Int64(1000),
	})
	r.NoError(err)

	// list-after-create: the new pool is present with the settings the create
	// applied, read back through ListPools (osd dump exposes these fields).
	listRes, err := client.ListPools(tstCtx, &pb.ListPoolsRequest{})
	r.NoError(err)
	pool := findPool(listRes.Pools, name)
	r.NotNil(pool, "created pool should appear in ListPools")
	fields := pool.Fields
	r.Equal(float64(2), fields["size"].GetNumberValue())
	r.Equal("on", fields["pg_autoscale_mode"].GetStringValue())
	r.Equal(float64(1000), fields["quota_max_objects"].GetNumberValue())
	// type int->string and crush_rule id->name transforms applied.
	r.Equal("replicated", fields["type"].GetStringValue())
	r.Equal("replicated_rule", fields["crush_rule"].GetStringValue())
	// application_metadata object -> list of keys.
	r.Contains(fields["application_metadata"].GetListValue().AsSlice(), "rbd")
}

func Test_Pool_List_AttrsWhitelist(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	res, err := client.ListPools(tstCtx, &pb.ListPoolsRequest{
		Attrs: proto.String("size,type"),
	})
	r.NoError(err)
	r.NotEmpty(res.Pools)
	for _, p := range res.Pools {
		// Only the whitelisted attrs plus the always-present pool_name.
		keys := make([]string, 0, len(p.Fields))
		for k := range p.Fields {
			keys = append(keys, k)
		}
		for _, k := range keys {
			r.Contains([]string{"size", "type", "pool_name"}, k, "unexpected attr %q under whitelist", k)
		}
		r.Contains(keys, "pool_name")
	}
}

func Test_Pool_List_StatsUnsupported(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	_, err := client.ListPools(tstCtx, &pb.ListPoolsRequest{
		Stats: proto.Bool(true),
	})
	r.Error(err)
	r.Contains(err.Error(), "Unimplemented")
}

func Test_Pool_List_Empty(t *testing.T) {
	// ListPools without args succeeds even when called by the bootstrap admin;
	// the response is a (possibly empty) slice, never an error.
	r := require.New(t)
	client := pb.NewPoolClient(admConn)
	_, err := client.ListPools(tstCtx, &pb.ListPoolsRequest{})
	r.NoError(err)
}

func Test_Pool_Create_MissingRequired(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		// Pool name missing.
		PgNum:    8,
		PoolType: "replicated",
	})
	r.Error(err)
	r.Contains(err.Error(), "InvalidArgument")
}

func Test_Pool_Create_InvalidPoolType(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		Pool:     "e2e-pool-bad-type",
		PgNum:    8,
		PoolType: "bogus",
	})
	r.Error(err)
	r.Contains(err.Error(), "InvalidArgument")
}

func Test_Pool_Create_ConfigurationUnsupported(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	cfg, err := structpb.NewStruct(map[string]any{"rbd_qos_iops_limit": "100"})
	r.NoError(err)
	_, err = client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		Pool:          "e2e-pool-cfg",
		PgNum:         8,
		PoolType:      "replicated",
		Configuration: cfg,
	})
	r.Error(err)
	r.Contains(err.Error(), "Unimplemented")
}

func Test_Pool_Create_PartialFailureLeavesPool(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	const name = "e2e-pool-partial"
	t.Cleanup(func() {
		cephEnv.Exec(context.Background(), []string{
			"ceph", "osd", "pool", "delete", name, name, "--yes-i-really-really-mean-it",
		})
	})

	// The command sequence is non-atomic (§11): osd pool create succeeds, then
	// an invalid pg_autoscale_mode value makes the follow-up osd pool set fail.
	// The handler returns the first error but the pool already exists. Pinning
	// this so a future rollback refactor can't silently change the contract.
	_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		Pool:            name,
		PgNum:           8,
		PoolType:        "replicated",
		PgAutoscaleMode: proto.String("bogus"),
	})
	r.Error(err)

	out, err := cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "ls", "-f", "json"})
	r.NoError(err)
	r.Contains(out, name)
}

func Test_Pool_Create_AlreadyExists(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	const name = "e2e-pool-dup"
	t.Cleanup(func() {
		cephEnv.Exec(context.Background(), []string{
			"ceph", "osd", "pool", "delete", name, name, "--yes-i-really-really-mean-it",
		})
	})

	req := &pb.CreatePoolRequest{Pool: name, PgNum: 8, PoolType: "replicated"}
	_, err := client.CreatePool(tstCtx, req)
	r.NoError(err)

	// osd pool create is idempotent on the mon side (returns rc=0 when the
	// pool already exists), matching the dashboard; a re-create must not error.
	_, err = client.CreatePool(tstCtx, req)
	r.NoError(err)
}
