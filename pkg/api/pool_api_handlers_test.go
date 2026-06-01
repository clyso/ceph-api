package api

import (
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func Test_poolCreateCommands(t *testing.T) {
	tests := []struct {
		name string
		req  *pb.CreatePoolRequest
		want []map[string]interface{}
	}{
		{
			name: "replicated with autoscale and size",
			req: &pb.CreatePoolRequest{
				Pool:                "testpool1",
				PgNum:               8,
				PoolType:            "replicated",
				RuleName:            proto.String("replicated_rule"),
				ApplicationMetadata: []string{"rbd"},
				PgAutoscaleMode:     proto.String("on"),
				Size:                proto.Int32(1),
			},
			want: []map[string]interface{}{
				{"prefix": "osd pool create", "format": "json", "pool": "testpool1", "pg_num": int32(8), "pgp_num": int32(8), "pool_type": "replicated", "rule": "replicated_rule"},
				{"prefix": "osd pool application enable", "format": "json", "pool": "testpool1", "app": "rbd", "yes_i_really_mean_it": true},
				{"prefix": "osd pool set", "format": "json", "pool": "testpool1", "var": "pg_autoscale_mode", "val": "on"},
				{"prefix": "osd pool set", "format": "json", "pool": "testpool1", "var": "size", "val": "1"},
			},
		},
		{
			name: "quota and compression",
			req: &pb.CreatePoolRequest{
				Pool:                 "testpool2",
				PgNum:                8,
				PoolType:             "replicated",
				QuotaMaxObjects:      proto.Int64(1000),
				QuotaMaxBytes:        proto.Int64(1073741824),
				CompressionMode:      proto.String("aggressive"),
				CompressionAlgorithm: proto.String("snappy"),
			},
			want: []map[string]interface{}{
				{"prefix": "osd pool create", "format": "json", "pool": "testpool2", "pg_num": int32(8), "pgp_num": int32(8), "pool_type": "replicated"},
				{"prefix": "osd pool set-quota", "format": "json", "pool": "testpool2", "field": "max_objects", "val": "1000"},
				{"prefix": "osd pool set-quota", "format": "json", "pool": "testpool2", "field": "max_bytes", "val": "1073741824"},
				{"prefix": "osd pool set", "format": "json", "pool": "testpool2", "var": "compression_mode", "val": "aggressive"},
				{"prefix": "osd pool set", "format": "json", "pool": "testpool2", "var": "compression_algorithm", "val": "snappy"},
			},
		},
		{
			name: "erasure with profile and ec_overwrites flag",
			req: &pb.CreatePoolRequest{
				Pool:                "ecpool",
				PgNum:               8,
				PoolType:            "erasure",
				ErasureCodeProfile:  proto.String("default"),
				Flags:               []string{"ec_overwrites"},
				ApplicationMetadata: []string{"rbd"},
			},
			want: []map[string]interface{}{
				{"prefix": "osd pool create", "format": "json", "pool": "ecpool", "pg_num": int32(8), "pgp_num": int32(8), "pool_type": "erasure", "erasure_code_profile": "default"},
				{"prefix": "osd pool set", "format": "json", "pool": "ecpool", "var": "allow_ec_overwrites", "val": "true"},
				{"prefix": "osd pool application enable", "format": "json", "pool": "ecpool", "app": "rbd", "yes_i_really_mean_it": true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tt.want, poolCreateCommands(tt.req))
		})
	}
}

func Test_poolCreateCommands_ratioStringified(t *testing.T) {
	r := require.New(t)
	cmds := poolCreateCommands(&pb.CreatePoolRequest{
		Pool:                     "p",
		PgNum:                    4,
		PoolType:                 "replicated",
		CompressionMode:          proto.String("aggressive"),
		CompressionRequiredRatio: proto.Float64(0.8),
		TargetSizeRatio:          proto.Float64(0.1),
	})
	want := map[string]string{
		"compression_required_ratio": "0.8",
		"target_size_ratio":          "0.1",
	}
	got := map[string]string{}
	for _, c := range cmds {
		if c["prefix"] == "osd pool set" {
			if v, ok := want[c["var"].(string)]; ok {
				got[c["var"].(string)] = c["val"].(string)
				r.Equal(v, c["val"], "var %s", c["var"])
			}
		}
	}
	r.Len(got, len(want))
}
