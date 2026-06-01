package api

import (
	"errors"
	"testing"

	"github.com/clyso/ceph-api/pkg/types"
	"github.com/stretchr/testify/require"
)

func Test_stringifyVal(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string passthrough", "on", "on"},
		{"integral float renders without decimal", float64(1000), "1000"},
		{"large integral float (byte count)", float64(1073741824), "1073741824"},
		{"fractional float", float64(0.85), "0.85"},
		{"bool true matches python str(True)", true, "True"},
		{"bool false matches python str(False)", false, "False"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, stringifyVal(tc.in))
		})
	}
}

type errnoErr struct {
	code int
	msg  string
}

func (e errnoErr) Error() string  { return e.msg }
func (e errnoErr) ErrorCode() int { return e.code }

func Test_mapMonError(t *testing.T) {
	cases := []struct {
		name     string
		in       error
		sentinel error // nil means error passes through unchanged
	}{
		{"einval to invalid arg", errnoErr{-22, "ec overwrites can only be enabled for an erasure coded pool"}, types.ErrInvalidArg},
		{"enoent to not found", errnoErr{-2, "no such pool"}, types.ErrNotFound},
		{"eexist to already exists", errnoErr{-17, "exists"}, types.ErrAlreadyExists},
		{"unknown errno passes through", errnoErr{-5, "io error"}, nil},
		{"plain error passes through", errors.New("boom"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			got := mapMonError(tc.in)
			if tc.sentinel == nil {
				r.Equal(tc.in, got)
				return
			}
			r.True(errors.Is(got, tc.sentinel))
			// Original ceph message is preserved for the client.
			r.Contains(got.Error(), tc.in.Error())
		})
	}
}

func Test_buildCreatePoolCommands_required(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing pool", map[string]any{"pg_num": float64(8), "pool_type": "replicated"}},
		{"empty pool", map[string]any{"pool": "", "pg_num": float64(8), "pool_type": "replicated"}},
		{"missing pg_num", map[string]any{"pool": "p", "pool_type": "replicated"}},
		{"missing pool_type", map[string]any{"pool": "p", "pg_num": float64(8)}},
		{"bad pool_type", map[string]any{"pool": "p", "pg_num": float64(8), "pool_type": "weird"}},
		{"non-numeric pg_num", map[string]any{"pool": "p", "pg_num": "eight", "pool_type": "replicated"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildCreatePoolCommands(tc.body)
			require.Error(t, err)
			require.True(t, errors.Is(err, types.ErrInvalidArg))
		})
	}
}

func Test_buildCreatePoolCommands_sequence(t *testing.T) {
	r := require.New(t)
	// Mirrors the audit-log sequence in tasks/post-api-pool.md §7.
	body := map[string]any{
		"pool":                 "testpool1",
		"pg_num":               float64(8),
		"pool_type":            "replicated",
		"rule_name":            "replicated_rule",
		"application_metadata": []any{"rbd"},
		"pg_autoscale_mode":    "on",
		"quota_max_bytes":      float64(1073741824),
		"quota_max_objects":    float64(1000),
	}

	cmds, err := buildCreatePoolCommands(body)
	r.NoError(err)
	r.Len(cmds, 5)

	r.Equal(map[string]any{
		"prefix": "osd pool create", "pool": "testpool1",
		"pg_num": 8, "pgp_num": 8, "pool_type": "replicated",
		"rule": "replicated_rule", "format": "json",
	}, cmds[0])

	r.Equal(map[string]any{
		"prefix": "osd pool application enable", "pool": "testpool1",
		"app": "rbd", "yes_i_really_mean_it": true, "format": "json",
	}, cmds[1])

	r.Equal(map[string]any{
		"prefix": "osd pool set-quota", "pool": "testpool1",
		"field": "max_objects", "val": "1000", "format": "json",
	}, cmds[2])

	r.Equal(map[string]any{
		"prefix": "osd pool set-quota", "pool": "testpool1",
		"field": "max_bytes", "val": "1073741824", "format": "json",
	}, cmds[3])

	r.Equal(map[string]any{
		"prefix": "osd pool set", "pool": "testpool1",
		"var": "pg_autoscale_mode", "val": "on", "format": "json",
	}, cmds[4])
}

func Test_buildCreatePoolCommands_ecOverwrites(t *testing.T) {
	r := require.New(t)
	body := map[string]any{
		"pool":      "ecpool",
		"pg_num":    float64(16),
		"pool_type": "erasure",
		"flags":     []any{"ec_overwrites"},
	}
	cmds, err := buildCreatePoolCommands(body)
	r.NoError(err)
	// create + ec_overwrites set
	r.Len(cmds, 2)
	r.Equal("osd pool create", cmds[0]["prefix"])
	r.Equal(map[string]any{
		"prefix": "osd pool set", "pool": "ecpool",
		"var": "allow_ec_overwrites", "val": "true", "format": "json",
	}, cmds[1])
}

func Test_buildCreatePoolCommands_pgNumZeroAccepted(t *testing.T) {
	r := require.New(t)
	// Faithful to the dashboard: int(pg_num) imposes no lower bound, so 0 is
	// accepted at the API layer and forwarded to mon (which is the authority
	// on validity). We deliberately do not add a bound the dashboard lacks.
	cmds, err := buildCreatePoolCommands(map[string]any{
		"pool":      "z",
		"pg_num":    float64(0),
		"pool_type": "replicated",
	})
	r.NoError(err)
	r.Equal(0, cmds[0]["pg_num"])
	r.Equal(0, cmds[0]["pgp_num"])
}

func Test_buildCreatePoolCommands_genericKwargs(t *testing.T) {
	r := require.New(t)
	// Arbitrary open kwargs of mixed types: a bool must stringify to python's
	// str(True)="True", a string passes through, and multiple kwargs are
	// emitted in sorted key order (deterministic, independent vars).
	body := map[string]any{
		"pool":              "kw",
		"pg_num":            float64(8),
		"pool_type":         "replicated",
		"bulk":              true,
		"size":              float64(3),
		"pg_autoscale_mode": "on",
	}
	cmds, err := buildCreatePoolCommands(body)
	r.NoError(err)
	// create + 3 sorted kwargs (bulk, pg_autoscale_mode, size)
	r.Len(cmds, 4)
	r.Equal("osd pool create", cmds[0]["prefix"])
	r.Equal(map[string]any{
		"prefix": "osd pool set", "pool": "kw",
		"var": "bulk", "val": "True", "format": "json",
	}, cmds[1])
	r.Equal(map[string]any{
		"prefix": "osd pool set", "pool": "kw",
		"var": "pg_autoscale_mode", "val": "on", "format": "json",
	}, cmds[2])
	r.Equal(map[string]any{
		"prefix": "osd pool set", "pool": "kw",
		"var": "size", "val": "3", "format": "json",
	}, cmds[3])
}

func Test_buildCreatePoolCommands_quotaOnly(t *testing.T) {
	r := require.New(t)
	// A body with only a byte quota and no app/kwargs: create + one set-quota
	// whose val is the stringified int64 byte count.
	body := map[string]any{
		"pool":            "q",
		"pg_num":          float64(8),
		"pool_type":       "replicated",
		"quota_max_bytes": float64(1073741824),
	}
	cmds, err := buildCreatePoolCommands(body)
	r.NoError(err)
	r.Len(cmds, 2)
	r.Equal("osd pool create", cmds[0]["prefix"])
	r.Equal(map[string]any{
		"prefix": "osd pool set-quota", "pool": "q",
		"field": "max_bytes", "val": "1073741824", "format": "json",
	}, cmds[1])
}

func Test_buildCreatePoolCommands_erasureCodeProfile(t *testing.T) {
	r := require.New(t)
	body := map[string]any{
		"pool":                 "ecpool",
		"pg_num":               float64(16),
		"pool_type":            "erasure",
		"erasure_code_profile": "myprofile",
	}
	cmds, err := buildCreatePoolCommands(body)
	r.NoError(err)
	r.Equal("myprofile", cmds[0]["erasure_code_profile"])
	// rule omitted when absent
	_, hasRule := cmds[0]["rule"]
	r.False(hasRule)
}
