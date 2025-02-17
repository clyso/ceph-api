//go:build !mock

package types

import (
	"github.com/ceph/go-ceph/rados"
)

const (
	// ErrNotFound is returned when an object is not found.
	RadosErrorNotFound = rados.ErrNotFound
)
