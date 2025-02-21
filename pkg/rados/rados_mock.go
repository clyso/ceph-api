//go:build mock

package rados

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"time"
)

//go:embed mock-data/mon/*.json mock-data/mon-input/*.json mock-data/mgr/*.json
var responsesFS embed.FS

const (
	baseDir = "mock-data"
)

// NewMockConn creates a new mock connection that loads JSON responses from files.
// It expects the configuration's MockDataDir to contain three subdirectories:
//   - "mon"         for ExecMon responses,
//   - "mon-input"   for ExecMonWithInputBuff responses,
//   - "mgr"         for ExecMgr responses.
func NewMockConn() (RadosConnInterface, error) {
	// Build the paths for each category within the embedded FS
	monDir := filepath.Join(baseDir, "mon")
	monInputDir := filepath.Join(baseDir, "mon-input")
	mgrDir := filepath.Join(baseDir, "mgr")

	// Load responses from the embedded FS
	monResponses, err := loadResponsesFromDir(monDir)
	if err != nil {
		return nil, fmt.Errorf("error loading mon responses: %v", err)
	}
	monInputResponses, err := loadResponsesFromDir(monInputDir)
	if err != nil {
		return nil, fmt.Errorf("error loading mon-input responses: %v", err)
	}
	mgrResponses, err := loadResponsesFromDir(mgrDir)
	if err != nil {
		return nil, fmt.Errorf("error loading mgr responses: %v", err)
	}

	return &MockConn{
		monResponses:      monResponses,
		monInputResponses: monInputResponses,
		mgrResponses:      mgrResponses,
		rng:               rand.New(rand.NewSource(time.Now().UnixNano())),
	}, nil
}

// MockConn implements RadosConnInterface and returns random responses from a set of preloaded JSON files
type MockConn struct {
	monResponses      map[string][][]byte
	monInputResponses map[string][][]byte
	mgrResponses      map[string][][]byte
	rng               *rand.Rand
}

// randomly selects a response from the given slice
func (mc *MockConn) selectRandomResponse(responses [][]byte) ([]byte, error) {
	if len(responses) == 0 {
		return nil, errors.New("no responses available")
	}
	idx := mc.rng.Intn(len(responses))
	return responses[idx], nil
}

// normalize extracts the "prefix" field from a JSON command and normalizes it
func normalize(cmdJSON []byte) (string, error) {
	var cmdData map[string]interface{}
	if err := json.Unmarshal(cmdJSON, &cmdData); err != nil {
		return "", fmt.Errorf("failed to parse command JSON: %w", err)
	}
	raw, ok := cmdData["prefix"]
	if !ok {
		return "", errors.New("command JSON missing 'prefix' field")
	}
	prefix, ok := raw.(string)
	if !ok || prefix == "" {
		return "", errors.New("prefix is not a valid string")
	}
	// Replace spaces with underscores to match the file names
	return strings.ReplaceAll(prefix, " ", "_"), nil
}

func (mc *MockConn) MonCommand(in []byte) ([]byte, string, error) {
	prefix, err := normalize(in)
	if err != nil {
		return nil, "", err
	}
	responses, exists := mc.monResponses[prefix]
	if !exists || len(responses) == 0 {
		return nil, "", fmt.Errorf("unknown monitor command prefix: %s", prefix)
	}
	resp, err := mc.selectRandomResponse(responses)
	return resp, "OK", err
}

func (mc *MockConn) MonCommandWithInputBuffer(cmd []byte, in []byte) ([]byte, string, error) {
	prefix, err := normalize(cmd)
	if err != nil {
		return nil, "", err
	}
	responses, exists := mc.monInputResponses[prefix]
	if !exists || len(responses) == 0 {
		return nil, "", fmt.Errorf("unknown monitor command with input prefix: %s", prefix)
	}
	resp, err := mc.selectRandomResponse(responses)
	return resp, "OK", err
}

func (mc *MockConn) MgrCommand(in [][]byte) ([]byte, string, error) {
	if len(in) == 0 {
		return nil, "", errors.New("no command provided")
	}
	prefix, err := normalize(in[0])
	if err != nil {
		return nil, "", err
	}
	responses, exists := mc.mgrResponses[prefix]
	if !exists || len(responses) == 0 {
		return nil, "", fmt.Errorf("unknown manager command prefix: %s", prefix)
	}
	resp, err := mc.selectRandomResponse(responses)
	return resp, "OK", err
}

func (mc *MockConn) Shutdown() {
	// No-op
}

// loadResponsesFromDir reads JSON response files from the embedded FS
func loadResponsesFromDir(dir string) (map[string][][]byte, error) {
	responsesMap := make(map[string][][]byte)

	entries, err := responsesFS.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		filePath := filepath.Join(dir, entry.Name())
		data, err := responsesFS.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
		}

		var jsonArray []json.RawMessage
		if err := json.Unmarshal(data, &jsonArray); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON from file %s: %w", filePath, err)
		}

		// Use the file name (without ".json") as the key.
		key := strings.TrimSuffix(entry.Name(), ".json")
		responses := make([][]byte, len(jsonArray))
		for i, raw := range jsonArray {
			responses[i] = []byte(raw)
		}
		responsesMap[key] = responses
	}

	return responsesMap, nil
}
