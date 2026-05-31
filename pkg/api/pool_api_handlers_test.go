package api

import (
	"encoding/json"
	"errors"
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
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

// Expected mapping per pool.py _serialize_pool: {1: 'replicated', 3: 'erasure'}.
func TestPoolTypeName(t *testing.T) {
	r := require.New(t)
	r.Equal("replicated", poolTypeName(1))
	r.Equal("erasure", poolTypeName(3))
	r.Equal("", poolTypeName(0))
	r.Equal("", poolTypeName(2))
}

// pool.py emits list(application_metadata.keys()); we sort for determinism.
func TestStructKeys(t *testing.T) {
	r := require.New(t)
	r.Nil(structKeys(nil))

	empty, err := structpb.NewStruct(map[string]any{})
	r.NoError(err)
	r.Empty(structKeys(empty))

	s, err := structpb.NewStruct(map[string]any{"rgw": map[string]any{}})
	r.NoError(err)
	r.Equal([]string{"rgw"}, structKeys(s))

	multi, err := structpb.NewStruct(map[string]any{"rbd": map[string]any{}, "cephfs": map[string]any{}})
	r.NoError(err)
	r.Equal([]string{"cephfs", "rbd"}, structKeys(multi))
}

// Ceph's JSON formatter streams non-finite doubles as bare inf/-inf/nan
// tokens (Formatter.cc add_value via stringstream); encoding/json rejects
// them. Both inf and -inf -> "Infinity" mirrors pool.py's read_balance
// shaping (math.isinf is sign-agnostic); nan -> "NaN" only so the unmarshal
// succeeds (the dashboard leaves nan as a bare token).
func TestSanitizeCephFloats(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]any
	}{
		{
			name: "object value inf",
			in:   `{"score_acting": inf, "score_stable": 1.5}`,
			want: map[string]any{"score_acting": "Infinity", "score_stable": 1.5},
		},
		{
			name: "negative inf and nan",
			in:   `{"a": -inf, "b": nan}`,
			want: map[string]any{"a": "Infinity", "b": "NaN"},
		},
		{
			name: "adjacent array tokens",
			in:   `{"v": [inf, inf, 1]}`,
			want: map[string]any{"v": []any{"Infinity", "Infinity", float64(1)}},
		},
		{
			name: "no tokens untouched",
			in:   `{"x": 1, "y": "definfnan"}`,
			want: map[string]any{"x": float64(1), "y": "definfnan"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			var got map[string]any
			r.NoError(json.Unmarshal(sanitizeCephFloats([]byte(tt.in)), &got))
			r.Equal(tt.want, got)
		})
	}
}

func TestSerializePool(t *testing.T) {
	r := require.New(t)
	appMeta, err := structpb.NewStruct(map[string]any{"rgw": map[string]any{}})
	r.NoError(err)

	entry := &poolListEntry{}
	entry.Pool = 1
	entry.PoolName = ".rgw.root"
	entry.Type = 3
	entry.CrushRule = 2
	entry.ApplicationMetadata = appMeta

	out := serializePool(entry, map[int32]string{2: "ec_rule"})
	r.Equal(int32(1), out.Pool)
	r.Equal(".rgw.root", out.PoolName)
	r.Equal("erasure", out.Type)
	r.Equal("ec_rule", out.CrushRule)
	r.Equal([]string{"rgw"}, out.ApplicationMetadata)
}

// serializePool is a ~60-field manual copy from OsdDumpPool->PoolInfo with no
// compile-time sync (the is_stretch_pool class of bug). This asserts a
// fully-populated osd-dump entry round-trips every PoolInfo field, so a future
// copy omission is caught here rather than only by the parity body diff.
func TestSerializePool_AllFieldsCopied(t *testing.T) {
	r := require.New(t)

	// Distinct non-zero values for every scalar so a dropped field (which
	// would leave the proto zero value) is caught by the assertions below.
	const entryJSON = `{
		"pool": 7,
		"pool_name": "fullpool",
		"create_time": "2026-05-31T15:25:04.647470-0000",
		"flags": 9001,
		"flags_names": "hashpspool,ec_overwrites",
		"type": 1,
		"size": 3,
		"min_size": 2,
		"crush_rule": 4,
		"peering_crush_bucket_count": 11,
		"peering_crush_bucket_target": 12,
		"peering_crush_bucket_barrier": 13,
		"peering_crush_bucket_mandatory_member": 14,
		"object_hash": 2,
		"pg_autoscale_mode": "warn",
		"pg_num": 32,
		"pg_placement_num": 33,
		"pg_placement_num_target": 34,
		"pg_num_target": 35,
		"pg_num_pending": 36,
		"last_pg_merge_meta": {"ready_epoch": 5, "source_pgid": "7.1"},
		"last_change": "100",
		"last_force_op_resend": "101",
		"last_force_op_resend_prenautilus": "102",
		"last_force_op_resend_preluminous": "103",
		"auid": 104,
		"snap_mode": "selfmanaged",
		"snap_seq": 105,
		"snap_epoch": 106,
		"pool_snaps": [],
		"removed_snaps": "[1~3]",
		"quota_max_bytes": 107,
		"quota_max_objects": 108,
		"tiers": [9, 10],
		"tier_of": 11,
		"read_tier": 12,
		"write_tier": 13,
		"cache_mode": "writeback",
		"target_max_bytes": 109,
		"target_max_objects": 110,
		"cache_target_dirty_ratio_micro": 111,
		"cache_target_dirty_high_ratio_micro": 112,
		"cache_target_full_ratio_micro": 113,
		"cache_min_flush_age": 114,
		"cache_min_evict_age": 115,
		"erasure_code_profile": "ecprofile",
		"hit_set_params": {"type": "bloom"},
		"hit_set_period": 116,
		"hit_set_count": 117,
		"use_gmt_hitset": true,
		"min_read_recency_for_promote": 118,
		"min_write_recency_for_promote": 119,
		"hit_set_grade_decay_rate": 120,
		"hit_set_search_last_n": 121,
		"grade_table": [],
		"stripe_width": 122,
		"expected_num_objects": 123,
		"fast_read": true,
		"options": {"pg_num_min": 8},
		"application_metadata": {"rgw": {}},
		"read_balance": {"score_type": "Fair distribution", "score_acting": 1.5},
		"is_stretch_pool": true
	}`

	var entry poolListEntry
	r.NoError(json.Unmarshal([]byte(entryJSON), &entry))

	out := serializePool(&entry, map[int32]string{4: "the_rule"})

	r.Equal(int32(7), out.Pool)
	r.Equal("fullpool", out.PoolName)
	r.NotNil(out.CreateTime)
	r.Equal(int64(9001), out.Flags)
	r.Equal("hashpspool,ec_overwrites", out.FlagsNames)
	r.Equal("replicated", out.Type)
	r.Equal(int32(3), out.Size)
	r.Equal(int32(2), out.MinSize)
	r.Equal("the_rule", out.CrushRule)
	r.Equal(int32(11), out.PeeringCrushBucketCount)
	r.Equal(int32(12), out.PeeringCrushBucketTarget)
	r.Equal(int32(13), out.PeeringCrushBucketBarrier)
	r.Equal(int32(14), out.PeeringCrushBucketMandatoryMember)
	r.Equal(int32(2), out.ObjectHash)
	r.Equal("warn", out.PgAutoscaleMode)
	r.Equal(int32(32), out.PgNum)
	r.Equal(int32(33), out.PgPlacementNum)
	r.Equal(int32(34), out.PgPlacementNumTarget)
	r.Equal(int32(35), out.PgNumTarget)
	r.Equal(int32(36), out.PgNumPending)
	r.NotNil(out.LastPgMergeMeta)
	r.Equal(int32(5), out.LastPgMergeMeta.ReadyEpoch)
	r.Equal("100", out.LastChange)
	r.Equal("101", out.LastForceOpResend)
	r.Equal("102", out.LastForceOpResendPrenautilus)
	r.Equal("103", out.LastForceOpResendPreluminous)
	r.Equal(uint64(104), out.Auid)
	r.Equal("selfmanaged", out.SnapMode)
	r.Equal(uint64(105), out.SnapSeq)
	r.Equal(uint64(106), out.SnapEpoch)
	r.Equal("[1~3]", out.RemovedSnaps)
	r.Equal(uint64(107), out.QuotaMaxBytes)
	r.Equal(uint64(108), out.QuotaMaxObjects)
	r.Equal([]int32{9, 10}, out.Tiers)
	r.Equal(int32(11), out.TierOf)
	r.Equal(int32(12), out.ReadTier)
	r.Equal(int32(13), out.WriteTier)
	r.Equal("writeback", out.CacheMode)
	r.Equal(uint64(109), out.TargetMaxBytes)
	r.Equal(uint64(110), out.TargetMaxObjects)
	r.Equal(uint64(111), out.CacheTargetDirtyRatioMicro)
	r.Equal(uint64(112), out.CacheTargetDirtyHighRatioMicro)
	r.Equal(uint64(113), out.CacheTargetFullRatioMicro)
	r.Equal(uint64(114), out.CacheMinFlushAge)
	r.Equal(uint64(115), out.CacheMinEvictAge)
	r.Equal("ecprofile", out.ErasureCodeProfile)
	r.NotNil(out.HitSetParams)
	r.Equal("bloom", out.HitSetParams.Type)
	r.Equal(uint64(116), out.HitSetPeriod)
	r.Equal(uint64(117), out.HitSetCount)
	r.True(out.UseGmtHitset)
	r.Equal(uint64(118), out.MinReadRecencyForPromote)
	r.Equal(uint64(119), out.MinWriteRecencyForPromote)
	r.Equal(uint64(120), out.HitSetGradeDecayRate)
	r.Equal(uint64(121), out.HitSetSearchLastN)
	r.Equal(uint64(122), out.StripeWidth)
	r.Equal(uint64(123), out.ExpectedNumObjects)
	r.True(out.FastRead)
	r.NotNil(out.Options)
	r.Contains(out.Options.Fields, "pg_num_min")
	r.Equal([]string{"rgw"}, out.ApplicationMetadata)
	r.NotNil(out.ReadBalance)
	r.Contains(out.ReadBalance.Fields, "score_type")
	r.True(out.IsStretchPool)
}
