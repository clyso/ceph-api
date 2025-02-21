//go:build mock

package app

import "github.com/clyso/ceph-api/pkg/rados"

var IsMock = true

func getRadosConnection(radosConfig rados.Config) (rados.RadosConnInterface, error) {
	return rados.NewMockConn()
}
