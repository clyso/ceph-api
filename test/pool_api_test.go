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

	// CREATE is the only ported pool endpoint, so the settings can't be read
	// back through gRPC yet — assert them via the ceph CLI.
	// TODO(crud-readback): replace with GetPool/ListPools once ported.
	out, err := cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "get", name, "size", "-f", "json"})
	r.NoError(err)
	r.Contains(out, `"size":2`)

	out, err = cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "get", name, "pg_autoscale_mode", "-f", "json"})
	r.NoError(err)
	r.Contains(out, `"pg_autoscale_mode":"on"`)

	out, err = cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "get-quota", name, "-f", "json"})
	r.NoError(err)
	r.Contains(out, `"quota_max_objects":1000`)

	out, err = cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "application", "get", name, "-f", "json"})
	r.NoError(err)
	r.Contains(out, "rbd")
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
