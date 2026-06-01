//go:build cgo

package test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_CephEnv_Exec(t *testing.T) {
	r := require.New(t)
	out, err := cephEnv.Exec(tstCtx, []string{"ceph", "health", "-f", "json"})
	r.NoError(err)
	r.NotEmpty(out)
	var health map[string]any
	r.NoError(json.Unmarshal([]byte(out), &health))
	r.Contains(health, "status")
}
