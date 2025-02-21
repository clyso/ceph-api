//go:build !mock

package app

import "github.com/clyso/ceph-api/pkg/rados"

var IsMock = false

func getRadosConnection(radosConfig rados.Config) (rados.RadosConnInterface, error) {
	return rados.NewRadosConn(radosConfig)
}
