//go:build cgo

package types

import (
	"github.com/ceph/go-ceph/rados"
)

var (
	ErrRadosNotFound = rados.ErrNotFound
)
