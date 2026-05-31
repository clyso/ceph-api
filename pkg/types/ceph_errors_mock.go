//go:build !cgo

package types

import "errors"

var (
	ErrRadosNotFound = errors.New("error not found")
)
