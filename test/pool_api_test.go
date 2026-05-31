//go:build cgo

package test

import (
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// The Pool service exposes only CreatePool; there is no DeletePool RPC to
// port yet, so created pools cannot be cleaned up from the test. They live on
// the shared test cluster until it is torn down. Pool names are fixed and
// `osd pool create` is idempotent at the mon, so reruns do not accumulate.
func Test_CreatePool(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)
	statusClient := pb.NewStatusClient(admConn)

	const name = "e2e-test-pool"
	const quotaBytes = int64(1073741824)
	const quotaObjects = int64(1000)

	_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		Pool:                name,
		PgNum:               8,
		PoolType:            "replicated",
		RuleName:            proto.String("replicated_rule"),
		ApplicationMetadata: []string{"rbd"},
		PgAutoscaleMode:     proto.String("on"),
		CompressionMode:     proto.String("aggressive"),
		QuotaMaxBytes:       proto.Int64(quotaBytes),
		QuotaMaxObjects:     proto.Int64(quotaObjects),
	})
	r.NoError(err)

	// Read the pool back via osd dump and confirm the follow-up mon commands
	// (quotas, application enable, compression) actually landed.
	dump, err := statusClient.GetCephOsdDump(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	var found *pb.OsdDumpPool
	for _, p := range dump.Pools {
		if p.PoolName == name {
			found = p
			break
		}
	}
	r.NotNil(found, "created pool must appear in osd dump")
	r.Equal(uint64(quotaBytes), found.QuotaMaxBytes, "quota_max_bytes follow-up must land")
	r.Equal(uint64(quotaObjects), found.QuotaMaxObjects, "quota_max_objects follow-up must land")
	r.NotNil(found.ApplicationMetadata, "application_metadata follow-up must land")
	r.Contains(found.ApplicationMetadata.AsMap(), "rbd", "rbd application must be enabled")

	// osd pool create is idempotent at the mon: recreating must not error.
	_, err = client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		Pool:     name,
		PgNum:    8,
		PoolType: "replicated",
	})
	r.NoError(err)
}

func Test_CreatePoolValidation(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		PgNum:    8,
		PoolType: "replicated",
	})
	r.Error(err)
	r.Contains(err.Error(), "InvalidArgument")

	_, err = client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		Pool:     "e2e-test-pool-novalidate",
		PoolType: "replicated",
	})
	r.Error(err)
	r.Contains(err.Error(), "InvalidArgument")

	_, err = client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		Pool:  "e2e-test-pool-novalidate",
		PgNum: 8,
	})
	r.Error(err)
	r.Contains(err.Error(), "InvalidArgument")

	// Invalid pool_type must be rejected up front with a clean 400, not an
	// opaque mon EINVAL.
	_, err = client.CreatePool(tstCtx, &pb.CreatePoolRequest{
		Pool:     "e2e-test-pool-novalidate",
		PgNum:    8,
		PoolType: "replication",
	})
	r.Error(err)
	r.Contains(err.Error(), "InvalidArgument")
}
