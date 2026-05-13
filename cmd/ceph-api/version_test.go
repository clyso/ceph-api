package main

import (
	"bytes"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	oldVersion := version
	oldCommit := commit
	version = "v1.2.3"
	commit = "abc123"
	t.Cleanup(func() {
		version = oldVersion
		commit = oldCommit
	})

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got, want := buf.String(), "ceph-api v1.2.3 (abc123)\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}
