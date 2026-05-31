package api

import (
	"errors"
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestBuildCreatePoolCommands_Replicated(t *testing.T) {
	r := require.New(t)
	req := &pb.CreatePoolRequest{
		Pool:                "probe_pool_repl",
		PgNum:               proto.Int32(8),
		PoolType:            "replicated",
		RuleName:            proto.String("replicated_rule"),
		ApplicationMetadata: []string{"rbd"},
		Options: map[string]string{
			"pg_autoscale_mode": "on",
			"size":              "2",
			"compression_mode":  "none",
			"quota_max_bytes":   "1073741824",
			"quota_max_objects": "1000",
		},
	}

	cmds, err := buildCreatePoolCommands(req)
	r.NoError(err)

	// Order per §7: create → application enable → quotas (objects, bytes) →
	// remaining osd pool set (sorted).
	r.Len(cmds, 7)

	r.Equal(map[string]interface{}{
		"prefix":    "osd pool create",
		"pool":      "probe_pool_repl",
		"pg_num":    int32(8),
		"pgp_num":   int32(8),
		"pool_type": "replicated",
		"rule":      "replicated_rule",
		"format":    "json",
	}, cmds[0])

	r.Equal(map[string]interface{}{
		"prefix":               "osd pool application enable",
		"pool":                 "probe_pool_repl",
		"app":                  "rbd",
		"yes_i_really_mean_it": true,
		"format":               "json",
	}, cmds[1])

	r.Equal(map[string]interface{}{
		"prefix": "osd pool set-quota",
		"pool":   "probe_pool_repl",
		"field":  "max_objects",
		"val":    "1000",
		"format": "json",
	}, cmds[2])

	r.Equal(map[string]interface{}{
		"prefix": "osd pool set-quota",
		"pool":   "probe_pool_repl",
		"field":  "max_bytes",
		"val":    "1073741824",
		"format": "json",
	}, cmds[3])

	// Remaining osd pool set, sorted: compression_mode, pg_autoscale_mode, size.
	r.Equal("compression_mode", cmds[4]["var"])
	r.Equal("none", cmds[4]["val"])
	r.Equal("pg_autoscale_mode", cmds[5]["var"])
	r.Equal("on", cmds[5]["val"])
	r.Equal("size", cmds[6]["var"])
	r.Equal("2", cmds[6]["val"])
}

func TestBuildCreatePoolCommands_Erasure(t *testing.T) {
	r := require.New(t)
	req := &pb.CreatePoolRequest{
		Pool:                "probe_pool_ec",
		PgNum:               proto.Int32(8),
		PoolType:            "erasure",
		ErasureCodeProfile:  proto.String("probeprof"),
		Flags:               proto.String("ec_overwrites"),
		ApplicationMetadata: []string{"rbd"},
	}

	cmds, err := buildCreatePoolCommands(req)
	r.NoError(err)
	r.Len(cmds, 3)

	r.Equal(map[string]interface{}{
		"prefix":               "osd pool create",
		"pool":                 "probe_pool_ec",
		"pg_num":               int32(8),
		"pgp_num":              int32(8),
		"pool_type":            "erasure",
		"erasure_code_profile": "probeprof",
		"format":               "json",
	}, cmds[0])

	r.Equal(map[string]interface{}{
		"prefix": "osd pool set",
		"pool":   "probe_pool_ec",
		"var":    "allow_ec_overwrites",
		"val":    "true",
		"format": "json",
	}, cmds[1])

	r.Equal("osd pool application enable", cmds[2]["prefix"])
}

// A `pg_num` key inside Options mirrors pgp_num, faithful to pool.py:244-245.
// On a real create pg_num arrives as the named field, not in Options, so this
// branch is unreachable from the POST /api/pool route; it exists only to keep
// the kwargs pass-through faithful for a future shared update path. The case
// is synthetic: it sets pg_num via Options to exercise the mirror branch in
// isolation.
func TestBuildCreatePoolCommands_PgNumOptionMirrorsPgpNum(t *testing.T) {
	r := require.New(t)
	req := &pb.CreatePoolRequest{
		Pool:     "p",
		PgNum:    proto.Int32(8),
		PoolType: "replicated",
		Options:  map[string]string{"pg_num": "16"},
	}
	cmds, err := buildCreatePoolCommands(req)
	r.NoError(err)
	r.Len(cmds, 3)
	r.Equal("pg_num", cmds[1]["var"])
	r.Equal("16", cmds[1]["val"])
	r.Equal("pgp_num", cmds[2]["var"])
	r.Equal("16", cmds[2]["val"])
}

func TestBuildCreatePoolCommands_OmitsEmptyOptionals(t *testing.T) {
	r := require.New(t)
	req := &pb.CreatePoolRequest{
		Pool:               "p",
		PgNum:              proto.Int32(8),
		PoolType:           "replicated",
		ErasureCodeProfile: proto.String(""),
		RuleName:           proto.String(""),
		Flags:              proto.String("some_other_flag"),
	}
	cmds, err := buildCreatePoolCommands(req)
	r.NoError(err)
	r.Len(cmds, 1)
	_, hasProfile := cmds[0]["erasure_code_profile"]
	r.False(hasProfile)
	_, hasRule := cmds[0]["rule"]
	r.False(hasRule)
}

func TestBuildCreatePoolCommands_Validation(t *testing.T) {
	tests := []struct {
		name    string
		req     *pb.CreatePoolRequest
		wantErr error
	}{
		{
			name:    "missing pool",
			req:     &pb.CreatePoolRequest{PgNum: proto.Int32(8), PoolType: "replicated"},
			wantErr: types.ErrInvalidArg,
		},
		{
			name:    "missing pg_num",
			req:     &pb.CreatePoolRequest{Pool: "p", PoolType: "replicated"},
			wantErr: types.ErrInvalidArg,
		},
		{
			name:    "missing pool_type",
			req:     &pb.CreatePoolRequest{Pool: "p", PgNum: proto.Int32(8)},
			wantErr: types.ErrInvalidArg,
		},
		{
			name:    "configuration not supported",
			req:     &pb.CreatePoolRequest{Pool: "p", PgNum: proto.Int32(8), PoolType: "replicated", Configuration: map[string]string{"conf_rbd_qos_bps_limit": "1"}},
			wantErr: types.ErrNotImplemented,
		},
		{
			name:    "rbd_mirroring not supported",
			req:     &pb.CreatePoolRequest{Pool: "p", PgNum: proto.Int32(8), PoolType: "replicated", RbdMirroring: proto.Bool(true)},
			wantErr: types.ErrNotImplemented,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			_, err := buildCreatePoolCommands(tt.req)
			r.Error(err)
			r.True(errors.Is(err, tt.wantErr))
		})
	}
}

func TestBuildCreatePoolCommands_RbdMirroringFalseAllowed(t *testing.T) {
	r := require.New(t)
	req := &pb.CreatePoolRequest{
		Pool:         "p",
		PgNum:        proto.Int32(8),
		PoolType:     "replicated",
		RbdMirroring: proto.Bool(false),
	}
	cmds, err := buildCreatePoolCommands(req)
	r.NoError(err)
	r.Len(cmds, 1)
}
