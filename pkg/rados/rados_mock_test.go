//go:build mock

package rados

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestNewMockConn(t *testing.T) {
	// Create temporary test directories
	tempDir := t.TempDir()
	monDir := filepath.Join(tempDir, "mon")
	monInputDir := filepath.Join(tempDir, "mon-input")
	mgrDir := filepath.Join(tempDir, "mgr")

	for _, dir := range []string{monDir, monInputDir, mgrDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create directory %s: %v", dir, err)
		}
	}

	// Create test response files
	testResponses := []json.RawMessage{
		json.RawMessage(`{"result": "success1"}`),
		json.RawMessage(`{"result": "success2"}`),
	}

	responseData, err := json.Marshal(testResponses)
	if err != nil {
		t.Fatalf("Failed to marshal test responses: %v", err)
	}

	// Write test files
	files := map[string]string{
		filepath.Join(monDir, "test_command.json"):      string(responseData),
		filepath.Join(monInputDir, "test_command.json"): string(responseData),
		filepath.Join(mgrDir, "test_command.json"):      string(responseData),
	}

	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write test file %s: %v", path, err)
		}
	}

	// Test connection creation
	conn, err := NewMockConn(tempDir)
	if err != nil {
		t.Fatalf("NewMockConn failed: %v", err)
	}

	mockConn, ok := conn.(*MockConn)
	if !ok {
		t.Fatal("NewMockConn did not return a *MockConn")
	}

	// Verify responses were loaded
	for _, responses := range []map[string][][]byte{
		mockConn.monResponses,
		mockConn.monInputResponses,
		mockConn.mgrResponses,
	} {
		if len(responses) != 1 {
			t.Errorf("Expected 1 response set, got %d", len(responses))
		}
		if _, exists := responses["test_command"]; !exists {
			t.Error("test_command response not found")
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "simple command",
			input:    `{"prefix": "test_command"}`,
			expected: "test_command",
			wantErr:  false,
		},
		{
			name:     "command with spaces",
			input:    `{"prefix": "test command"}`,
			expected: "test_command",
			wantErr:  false,
		},
		{
			name:     "missing prefix",
			input:    `{"command": "test"}`,
			expected: "",
			wantErr:  true,
		},
		{
			name:     "invalid JSON",
			input:    `{"prefix": }`,
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := normalize([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("normalize() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if result != tt.expected {
				t.Errorf("normalize() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestMockConnCommands(t *testing.T) {
	// Create a mock connection with test data
	mockConn := &MockConn{
		monResponses: map[string][][]byte{
			"test_command": {[]byte(`{"result": "success"}`)},
		},
		monInputResponses: map[string][][]byte{
			"test_command": {[]byte(`{"result": "success_with_input"}`)},
		},
		mgrResponses: map[string][][]byte{
			"test_command": {[]byte(`{"result": "mgr_success"}`)},
		},
		rng: rand.New(rand.NewSource(1)), // Fixed seed for reproducible tests
	}

	// Test MonCommand
	t.Run("MonCommand", func(t *testing.T) {
		resp, status, err := mockConn.MonCommand([]byte(`{"prefix": "test_command"}`))
		if err != nil {
			t.Errorf("MonCommand failed: %v", err)
		}
		if status != "OK" {
			t.Errorf("Expected status OK, got %s", status)
		}
		var result map[string]string
		if err := json.Unmarshal(resp, &result); err != nil {
			t.Errorf("Failed to unmarshal response: %v", err)
		}
		if result["result"] != "success" {
			t.Errorf("Expected result 'success', got %s", result["result"])
		}
	})

	// Test MonCommandWithInputBuffer
	t.Run("MonCommandWithInputBuffer", func(t *testing.T) {
		resp, status, err := mockConn.MonCommandWithInputBuffer(
			[]byte(`{"prefix": "test_command"}`),
			[]byte("input data"),
		)
		if err != nil {
			t.Errorf("MonCommandWithInputBuffer failed: %v", err)
		}
		if status != "OK" {
			t.Errorf("Expected status OK, got %s", status)
		}
		var result map[string]string
		if err := json.Unmarshal(resp, &result); err != nil {
			t.Errorf("Failed to unmarshal response: %v", err)
		}
		if result["result"] != "success_with_input" {
			t.Errorf("Expected result 'success_with_input', got %s", result["result"])
		}
	})

	// Test MgrCommand
	t.Run("MgrCommand", func(t *testing.T) {
		resp, status, err := mockConn.MgrCommand([][]byte{[]byte(`{"prefix": "test_command"}`)})
		if err != nil {
			t.Errorf("MgrCommand failed: %v", err)
		}
		if status != "OK" {
			t.Errorf("Expected status OK, got %s", status)
		}
		var result map[string]string
		if err := json.Unmarshal(resp, &result); err != nil {
			t.Errorf("Failed to unmarshal response: %v", err)
		}
		if result["result"] != "mgr_success" {
			t.Errorf("Expected result 'mgr_success', got %s", result["result"])
		}
	})

	// Test error cases
	t.Run("Unknown command", func(t *testing.T) {
		_, _, err := mockConn.MonCommand([]byte(`{"prefix": "unknown_command"}`))
		if err == nil {
			t.Error("Expected error for unknown command, got nil")
		}
	})

	t.Run("Empty MgrCommand", func(t *testing.T) {
		_, _, err := mockConn.MgrCommand([][]byte{})
		if err == nil {
			t.Error("Expected error for empty MgrCommand, got nil")
		}
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		_, _, err := mockConn.MonCommand([]byte(`{invalid json`))
		if err == nil {
			t.Error("Expected error for invalid JSON, got nil")
		}
	})
}

func TestNewRadosConn(t *testing.T) {
	conn, err := NewRadosConn(Config{})
	if err == nil {
		t.Error("Expected error from NewRadosConn in mock build")
	}
	if conn != nil {
		t.Error("Expected nil connection from NewRadosConn in mock build")
	}
}
