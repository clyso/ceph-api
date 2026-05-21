//go:build !cgo

package test

import (
	"fmt"
	"os"
	"testing"
)

func runSetup(m *testing.M) (int, error) {
	fmt.Fprintln(os.Stderr, "test: e2e tests skipped (cgo disabled); use `make e2e-test` or `go test ./test/ -tid` to run them in Docker with ceph dev libs")
	return m.Run(), nil
}
