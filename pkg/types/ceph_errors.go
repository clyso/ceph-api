//go:build cgo

package types

import (
	"github.com/ceph/go-ceph/rados"
)

const (
	ErrRadosNotFound = rados.ErrNotFound
)
