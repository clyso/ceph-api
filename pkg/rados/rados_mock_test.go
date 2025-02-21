//go:build mock

package rados

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestMockConnAllEmbeddedCommands(t *testing.T) {
	conn, err := NewMockConn()
	if err != nil {
		t.Fatalf("NewMockConn() error: %v", err)
	}
	mockConn, ok := conn.(*MockConn)
	if !ok {
		t.Fatal("NewMockConn did not return a *MockConn")
	}

	monCommands := []string{
		"config-key get",
		"mon dump",
		"osd crush dump",
		"osd dump",
		"pg dump",
		"report",
		"status",
	}

	// Test each mon command in a subtest.
	for _, prefix := range monCommands {
		t.Run("MonCommand: "+prefix, func(t *testing.T) {
			cmd := []byte(fmt.Sprintf(`{"prefix": "%s"}`, prefix))
			resp, status, err := mockConn.MonCommand(cmd)
			if err != nil {
				t.Fatalf("MonCommand(%q) error: %v", prefix, err)
			}
			if status != "OK" {
				t.Errorf("MonCommand(%q) got status %q, want %q", prefix, status, "OK")
			}
			// Optionally parse and inspect the JSON response.
			var obj interface{}
			if err := json.Unmarshal(resp, &obj); err != nil {
				t.Errorf("Response for %q was not valid JSON: %v", prefix, err)
			}
		})
	}

	// Test mon command with input buffer.
	t.Run("MonCommandWithInputBuffer: config-key set", func(t *testing.T) {
		cmd := []byte(`{"prefix": "config-key set"}`)
		inputBuf := []byte(`example input data`)
		resp, status, err := mockConn.MonCommandWithInputBuffer(cmd, inputBuf)
		if err != nil {
			t.Fatalf("MonCommandWithInputBuffer(%q) error: %v", "config-key set", err)
		}
		if status != "OK" {
			t.Errorf("MonCommandWithInputBuffer(%q) got status %q, want %q", "config-key set", status, "OK")
		}
		var obj interface{}
		if err := json.Unmarshal(resp, &obj); err != nil {
			t.Errorf("Response for %q was not valid JSON: %v", "config-key set", err)
		}
	})
}
