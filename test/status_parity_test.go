//go:build cgo

package test

import (
	"testing"

	"github.com/clyso/ceph-api/test/parity"
	"github.com/stretchr/testify/require"
)

// /api/status/* has no dashboard counterpart - the dashboard exposes
// /api/health/* and /api/monitor instead. r.Backends(call) returns
// [Ours] only for these, so the parity recorder exercises ceph-api
// for coverage but doesn't try to hit the dashboard.

func Test_Parity_Status_Ceph(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/status/ceph"}
	for _, b := range r.Backends(call) {
		resp, _ := r.DoRecord(b, call)
		require.True(t, resp.StatusCode/100 == 2, "%s: %s %s: status %d", b, call.Method, call.Path, resp.StatusCode)
	}
}

func Test_Parity_Status_MonDump(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/status/mon_dump"}
	for _, b := range r.Backends(call) {
		resp, _ := r.DoRecord(b, call)
		require.True(t, resp.StatusCode/100 == 2, "%s: %s %s: status %d", b, call.Method, call.Path, resp.StatusCode)
	}
}

func Test_Parity_Status_OsdDump(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/status/osd_dump"}
	for _, b := range r.Backends(call) {
		resp, _ := r.DoRecord(b, call)
		require.True(t, resp.StatusCode/100 == 2, "%s: %s %s: status %d", b, call.Method, call.Path, resp.StatusCode)
	}
}

func Test_Parity_Status_PgDump(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/status/pg_dump"}
	for _, b := range r.Backends(call) {
		resp, _ := r.DoRecord(b, call)
		require.True(t, resp.StatusCode/100 == 2, "%s: %s %s: status %d", b, call.Method, call.Path, resp.StatusCode)
	}
}

func Test_Parity_Status_Report(t *testing.T) {
	r := parity.New(t)
	call := parity.Call{Method: "GET", Path: "/api/status/report"}
	for _, b := range r.Backends(call) {
		resp, _ := r.DoRecord(b, call)
		require.True(t, resp.StatusCode/100 == 2, "%s: %s %s: status %d", b, call.Method, call.Path, resp.StatusCode)
	}
}
