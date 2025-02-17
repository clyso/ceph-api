//go:build !mock

package rados

import (
	"strconv"

	"github.com/ceph/go-ceph/rados"
)

// ProductionConn wraps the real Ceph connection.
type ProductionConn struct {
	*rados.Conn
}

// Ensure ProductionConn implements ConnInterface.
var _ RadosConnInterface = (*ProductionConn)(nil)

// New creates a new Svc with a production connection.
func NewRadosConn(conf Config) (RadosConnInterface, error) {
	// Create a real connection.
	conn, err := rados.NewConnWithUser(conf.User)
	if err != nil {
		return nil, err
	}
	if conf.MonHost == "" || conf.UserKeyring == "" || conf.RadosTimeout == 0 {
		err = conn.ReadDefaultConfigFile()
	} else {
		err = conn.ParseCmdLineArgs([]string{"--mon-host", conf.MonHost, "--key", conf.UserKeyring, "--client_mount_timeout", "3"})
	}
	if err != nil {
		return nil, err
	}

	timeout := strconv.FormatFloat(conf.RadosTimeout.Seconds(), 'f', -1, 64)
	if err = conn.SetConfigOption("rados_osd_op_timeout", timeout); err != nil {
		return nil, err
	}
	if err = conn.SetConfigOption("rados_mon_op_timeout", timeout); err != nil {
		return nil, err
	}
	if err = conn.Connect(); err != nil {
		return nil, err
	}

	// Wrap the real connection.
	prodConn := &ProductionConn{Conn: conn}
	return prodConn, nil
}

// stub for mock connection
func NewMockConn(baseDir string) (RadosConnInterface, error) {
	return nil, nil
}
