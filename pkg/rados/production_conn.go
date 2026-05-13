//go:build !mock

package rados

import (
	"fmt"
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
		return nil, fmt.Errorf("initialize Ceph/RADOS client for user %q: ensure Ceph libraries are installed and available: %w", conf.User, err)
	}
	if conf.MonHost == "" || conf.UserKeyring == "" || conf.RadosTimeout == 0 {
		err = conn.ReadDefaultConfigFile()
	} else {
		err = conn.ParseCmdLineArgs([]string{"--mon-host", conf.MonHost, "--key", conf.UserKeyring, "--client_mount_timeout", "3"})
	}
	if err != nil {
		return nil, fmt.Errorf("load Ceph/RADOS configuration: provide rados.monHost and rados.userKeyring, or ensure the default Ceph config/keyring files are readable: %w", err)
	}

	timeout := strconv.FormatFloat(conf.RadosTimeout.Seconds(), 'f', -1, 64)
	if err = conn.SetConfigOption("rados_osd_op_timeout", timeout); err != nil {
		return nil, fmt.Errorf("configure Ceph/RADOS OSD operation timeout: %w", err)
	}
	if err = conn.SetConfigOption("rados_mon_op_timeout", timeout); err != nil {
		return nil, fmt.Errorf("configure Ceph/RADOS monitor operation timeout: %w", err)
	}
	if err = conn.Connect(); err != nil {
		return nil, fmt.Errorf("connect to Ceph/RADOS cluster: ensure Ceph config/keyring are valid and monitors are reachable: %w", err)
	}

	// Wrap the real connection.
	prodConn := &ProductionConn{Conn: conn}
	return prodConn, nil
}
