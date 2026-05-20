//go:build !cgo

package test

import (
	"errors"
	"testing"
)

func runSetup(_ *testing.M) (int, error) {
	return 1, errors.New("CGo required to run e2e tests directly; use `go test ./test/... -tid` to run them inside a Docker container with ceph dev libs")
}
