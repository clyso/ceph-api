//go:build cgo

package test

import (
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func Test_CreatePool(t *testing.T) {
	r := require.New(t)
	client := pb.NewPoolClient(admConn)

	t.Run("create replicated pool", func(t *testing.T) {
		_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
			Pool:                "e2e_pool_repl",
			PgNum:               proto.Int32(8),
			PoolType:            "replicated",
			RuleName:            proto.String("replicated_rule"),
			ApplicationMetadata: []string{"rbd"},
			Options: map[string]string{
				"size":              "2",
				"pg_autoscale_mode": "on",
				"compression_mode":  "none",
				"quota_max_bytes":   "1073741824",
				"quota_max_objects": "1000",
			},
		})
		r.NoError(err)
	})

	t.Run("created pool has the requested settings applied", func(t *testing.T) {
		// Read pool state back through the container CLI in JSON, independent of
		// the API under test, so a dropped set/set-quota command fails the test.
		get := func(field string) string {
			out, err := cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "get", "e2e_pool_repl", field, "-f", "json"})
			r.NoError(err)
			return out
		}
		r.Contains(get("size"), `"size":2`)
		r.Contains(get("pg_autoscale_mode"), `"pg_autoscale_mode":"on"`)
		r.Contains(get("compression_mode"), `"compression_mode":"none"`)

		quota, err := cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "get-quota", "e2e_pool_repl", "-f", "json"})
		r.NoError(err)
		r.Contains(quota, `"quota_max_bytes":1073741824`)
		r.Contains(quota, `"quota_max_objects":1000`)

		apps, err := cephEnv.Exec(tstCtx, []string{"ceph", "osd", "pool", "application", "get", "e2e_pool_repl", "-f", "json"})
		r.NoError(err)
		r.Contains(apps, "rbd")
	})

	t.Run("list includes the created pool with shaped fields", func(t *testing.T) {
		resp, err := client.ListPools(tstCtx, &pb.ListPoolsRequest{})
		r.NoError(err)

		var got *pb.PoolInfo
		for _, p := range resp.Pools {
			if p.PoolName == "e2e_pool_repl" {
				got = p
				break
			}
		}
		r.NotNil(got, "created pool must appear in the list")

		// type int->string and crush_rule id->name shaping.
		r.Equal("replicated", got.Type)
		r.Equal("replicated_rule", got.CrushRule)
		// application_metadata dict->list shaping.
		r.Equal([]string{"rbd"}, got.ApplicationMetadata)
		r.NotNil(got.CreateTime)
	})

	t.Run("stats=true is not implemented", func(t *testing.T) {
		_, err := client.ListPools(tstCtx, &pb.ListPoolsRequest{Stats: proto.Bool(true)})
		r.Error(err)
		r.Contains(err.Error(), "Unimplemented")
	})

	t.Run("create is idempotent", func(t *testing.T) {
		_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
			Pool:     "e2e_pool_repl",
			PgNum:    proto.Int32(8),
			PoolType: "replicated",
		})
		r.NoError(err)
	})

	t.Run("missing pg_num is rejected", func(t *testing.T) {
		_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
			Pool:     "e2e_pool_nopg",
			PoolType: "replicated",
		})
		r.Error(err)
		r.Contains(err.Error(), "InvalidArgument")
	})

	t.Run("missing pool is rejected", func(t *testing.T) {
		_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
			PgNum:    proto.Int32(8),
			PoolType: "replicated",
		})
		r.Error(err)
		r.Contains(err.Error(), "InvalidArgument")
	})

	t.Run("configuration is not implemented", func(t *testing.T) {
		_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
			Pool:          "e2e_pool_conf",
			PgNum:         proto.Int32(8),
			PoolType:      "replicated",
			Configuration: map[string]string{"conf_rbd_qos_bps_limit": "1024"},
		})
		r.Error(err)
		r.Contains(err.Error(), "Unimplemented")
	})

	t.Run("rbd_mirroring is not implemented", func(t *testing.T) {
		_, err := client.CreatePool(tstCtx, &pb.CreatePoolRequest{
			Pool:         "e2e_pool_mirror",
			PgNum:        proto.Int32(8),
			PoolType:     "replicated",
			RbdMirroring: proto.Bool(true),
		})
		r.Error(err)
		r.Contains(err.Error(), "Unimplemented")
	})
}
