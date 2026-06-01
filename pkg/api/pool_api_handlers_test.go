package api

import (
	"encoding/json"
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
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

func Test_serializePool_transforms(t *testing.T) {
	r := require.New(t)
	crushRules := map[int]string{0: "replicated_rule", 1: "ec_rule"}
	pool := map[string]interface{}{
		"pool":                 float64(1),
		"pool_name":            ".rgw.root",
		"type":                 float64(1),
		"crush_rule":           float64(0),
		"application_metadata": map[string]interface{}{"rgw": map[string]interface{}{}},
		"read_balance":         map[string]interface{}{"score_acting": float64(1)},
		"size":                 float64(3),
	}

	got := serializePool(pool, nil, crushRules)

	// type int -> string (1 -> "replicated"), pool.py:111.
	r.Equal("replicated", got["type"])
	// crush_rule id -> name, pool.py:113.
	r.Equal("replicated_rule", got["crush_rule"])
	// application_metadata object -> list of keys, pool.py:115.
	r.Equal([]interface{}{"rgw"}, got["application_metadata"])
	// read_balance with finite values passes through unchanged, pool.py:117-124.
	r.Equal(map[string]interface{}{"score_acting": float64(1)}, got["read_balance"])
	// untransformed key kept as-is.
	r.Equal(float64(3), got["size"])
	r.Equal(".rgw.root", got["pool_name"])
}

func Test_serializePool_erasureType(t *testing.T) {
	r := require.New(t)
	got := serializePool(map[string]interface{}{
		"pool_name": "ec",
		"type":      float64(3),
	}, nil, nil)
	// 3 -> "erasure", pool.py:111.
	r.Equal("erasure", got["type"])
}

func Test_serializePool_attrsWhitelist(t *testing.T) {
	r := require.New(t)
	pool := map[string]interface{}{
		"pool_name":  "p",
		"type":       float64(1),
		"size":       float64(3),
		"min_size":   float64(1),
		"crush_rule": float64(0),
	}
	// Whitelist excludes pool_name, size, min_size; pool_name must still appear
	// (pool.py:128-129), and transforms still apply to whitelisted keys.
	got := serializePool(pool, []string{"type"}, map[int]string{0: "replicated_rule"})
	r.Equal(map[string]interface{}{
		"type":      "replicated",
		"pool_name": "p",
	}, got)
}

func Test_serializePool_attrsMissingKeyIgnored(t *testing.T) {
	r := require.New(t)
	// A whitelisted attr absent from the pool is skipped (pool.py:108-109).
	got := serializePool(map[string]interface{}{"pool_name": "p"}, []string{"nonexistent"}, nil)
	r.Equal(map[string]interface{}{"pool_name": "p"}, got)
}

func Test_parsePoolAttrs(t *testing.T) {
	r := require.New(t)
	r.Nil(parsePoolAttrs(nil))
	r.Nil(parsePoolAttrs(proto.String("")))
	r.Equal([]string{"size", "type"}, parsePoolAttrs(proto.String("size,type")))
}

func Test_sanitizeCephFloats(t *testing.T) {
	r := require.New(t)
	// Bare inf/-inf/nan/-nan tokens (src/common/Formatter.cc dump_float) are
	// rewritten to valid JSON strings: inf/-inf -> "Infinity", nan/-nan -> "NaN".
	raw := []byte(`{"a": inf, "b": -inf, "c": nan, "n": -nan, "arr": [inf, 1.0], "info": "x", "d": 2.0}`)
	sanitized := sanitizeCephFloats(raw)

	var out map[string]interface{}
	r.NoError(json.Unmarshal(sanitized, &out))
	r.Equal("Infinity", out["a"])
	r.Equal("Infinity", out["b"])
	r.Equal("NaN", out["c"])
	// -nan must be sanitized too; libstdc++ emits negative NaN as bare -nan.
	r.Equal("NaN", out["n"])
	r.Equal([]interface{}{"Infinity", float64(1)}, out["arr"])
	// "info" must not be corrupted by the inf match (word boundary / quoted).
	r.Equal("x", out["info"])
	r.Equal(float64(2), out["d"])
}

// Test_serializePool_readBalanceInf pins the end-to-end inf transform: raw osd
// dump bytes carrying a bare inf (and -nan) read_balance score must survive
// sanitize -> unmarshal -> serializePool with inf landing as "Infinity" and the
// whole call never erroring on the -nan gap (D1/D6).
func Test_serializePool_readBalanceInf(t *testing.T) {
	r := require.New(t)
	raw := []byte(`{"pools": [{"pool_name": "p", "type": 1, "read_balance": {"score_type": "Fair distribution", "score_acting": inf, "primary_affinity_weighted": -nan, "raw_score_acting": 1.5}}]}`)

	var osdDump struct {
		Pools []map[string]interface{} `json:"pools"`
	}
	r.NoError(json.Unmarshal(sanitizeCephFloats(raw), &osdDump))
	r.Len(osdDump.Pools, 1)

	got := serializePool(osdDump.Pools[0], nil, nil)
	rb := got["read_balance"].(map[string]interface{})
	r.Equal("Infinity", rb["score_acting"])
	r.Equal("NaN", rb["primary_affinity_weighted"])
	r.Equal(float64(1.5), rb["raw_score_acting"])
	r.Equal("Fair distribution", rb["score_type"])

	// The serialized pool must round-trip into a protobuf Struct (the wire type).
	_, err := structpb.NewStruct(got)
	r.NoError(err)
}
