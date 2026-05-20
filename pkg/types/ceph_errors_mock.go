//go:build !cgo

package types

import "fmt"

var (
	// ErrNotFound is returned when an object is not found.
	RadosErrorNotFound = fmt.Errorf("Error Not Found")
)
