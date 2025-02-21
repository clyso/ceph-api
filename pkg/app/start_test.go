//go:build !mock

package app

import (
	"errors"
	"strconv"
	"testing"

	"github.com/ceph/go-ceph/rados"
	"github.com/stretchr/testify/require"
)

func Test_setupRadosConn(t *testing.T) {
	t.Skip()
	r := require.New(t)
	conn, err := rados.NewConnWithUser("admin")
	r.NoError(err)

	err = conn.ReadDefaultConfigFile()
	r.NoError(err)

	timeout := strconv.FormatFloat(3, 'f', -1, 64)

	err = conn.SetConfigOption("rados_osd_op_timeout", timeout)
	r.NoError(err)

	err = conn.SetConfigOption("rados_mon_op_timeout", timeout)
	r.NoError(err)

	err = conn.Connect()
	r.NoError(err)
	cmd := `{"prefix":"config-key get", "key":"mgr/dashboard/accessdb_v2"}`
	res, _, err := conn.MonCommand([]byte(cmd))
	t.Log(cmd)
	t.Log(string(res), err)
	if errors.Is(err, rados.ErrNotFound) {
		t.Log("asdfasdfasdfasdf")
	}
}
