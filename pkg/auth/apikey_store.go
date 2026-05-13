package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/clyso/ceph-api/pkg/types"
)

const apiKeyConfigPrefix = "ceph-api/auth/apikeys/"

type APIKeyStore struct {
	mon          MonCommander
	mu           sync.Mutex
	lastUsedSeen map[string]time.Time
}

func NewAPIKeyStore(mon MonCommander) *APIKeyStore {
	return &APIKeyStore{mon: mon, lastUsedSeen: map[string]time.Time{}}
}

func (s *APIKeyStore) Create(ctx context.Context, rec APIKeyRecord) error {
	if !validAPIKeyID(rec.ID) {
		return fmt.Errorf("%w: invalid API key id", types.ErrInvalidArg)
	}
	if _, err := s.Get(ctx, rec.ID); err == nil {
		return types.ErrAlreadyExists
	} else if !errors.Is(err, types.RadosErrorNotFound) && !errors.Is(err, types.ErrNotFound) {
		return err
	}
	return s.store(ctx, rec)
}

func (s *APIKeyStore) Get(ctx context.Context, id string) (APIKeyRecord, error) {
	if !validAPIKeyID(id) {
		return APIKeyRecord{}, fmt.Errorf("%w: invalid API key id", types.ErrInvalidArg)
	}
	cmd, err := json.Marshal(map[string]string{"prefix": "config-key get", "key": apiKeyConfigKey(id)})
	if err != nil {
		return APIKeyRecord{}, err
	}
	raw, err := s.mon.ExecMon(ctx, string(cmd))
	if err != nil {
		if errors.Is(err, types.RadosErrorNotFound) {
			return APIKeyRecord{}, types.ErrNotFound
		}
		return APIKeyRecord{}, err
	}

	return decodeAPIKeyRecord(id, raw)
}

func (s *APIKeyStore) List(ctx context.Context) ([]APIKeyRecord, error) {
	cmd, err := json.Marshal(map[string]string{"prefix": "config-key dump", "key": apiKeyConfigPrefix})
	if err != nil {
		return nil, err
	}
	raw, err := s.mon.ExecMon(ctx, string(cmd))
	if err != nil {
		return nil, err
	}
	records, err := decodeConfigKeyDump(raw)
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, nil
}

func (s *APIKeyStore) Revoke(ctx context.Context, id string, now time.Time) (APIKeyRecord, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return APIKeyRecord{}, err
	}
	rec.Enabled = false
	rec.RevokedAt = &now
	if err := s.store(ctx, rec); err != nil {
		return APIKeyRecord{}, err
	}
	return rec, nil
}

func (s *APIKeyStore) TouchLastUsed(ctx context.Context, rec APIKeyRecord, now time.Time) error {
	if rec.LastUsedAt != nil && rec.LastUsedAt.After(now.Add(-apiKeyLastUsedDebounce)) {
		return nil
	}
	latest, err := s.Get(ctx, rec.ID)
	if err != nil {
		return err
	}
	if latest.LastUsedAt != nil && latest.LastUsedAt.After(now.Add(-apiKeyLastUsedDebounce)) {
		return nil
	}
	latest.LastUsedAt = &now
	return s.store(ctx, latest)
}

func (s *APIKeyStore) shouldTouchLastUsed(rec APIKeyRecord, now time.Time) bool {
	threshold := now.Add(-apiKeyLastUsedDebounce)
	if rec.LastUsedAt != nil && rec.LastUsedAt.After(threshold) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastUsedSeen == nil {
		s.lastUsedSeen = map[string]time.Time{}
	}
	if last, ok := s.lastUsedSeen[rec.ID]; ok && last.After(threshold) {
		return false
	}
	s.lastUsedSeen[rec.ID] = now
	return true
}

func (s *APIKeyStore) store(ctx context.Context, rec APIKeyRecord) error {
	val, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	env, err := json.Marshal(keyStoreEnvelope{Version: 1, Value: json.RawMessage(val)})
	if err != nil {
		return err
	}
	cmd, err := json.Marshal(map[string]string{"prefix": "config-key set", "key": apiKeyConfigKey(rec.ID)})
	if err != nil {
		return err
	}
	_, err = s.mon.ExecMonWithInputBuff(ctx, string(cmd), env)
	if err != nil {
		return fmt.Errorf("persist API key: %w", err)
	}
	return nil
}

func apiKeyConfigKey(id string) string {
	return apiKeyConfigPrefix + id
}

func decodeAPIKeyRecord(id string, raw []byte) (APIKeyRecord, error) {
	var env keyStoreEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return APIKeyRecord{}, fmt.Errorf("decode persisted API key envelope: %w", err)
	}
	if env.Version == 0 || len(env.Value) == 0 {
		return APIKeyRecord{}, fmt.Errorf("decode persisted API key envelope: missing value")
	}
	var rec APIKeyRecord
	if err := json.Unmarshal(env.Value, &rec); err != nil {
		return APIKeyRecord{}, fmt.Errorf("decode persisted API key: %w", err)
	}
	if rec.ID != id {
		return APIKeyRecord{}, fmt.Errorf("persisted API key id mismatch")
	}
	return rec, nil
}

func decodeConfigKeyDump(raw []byte) ([]APIKeyRecord, error) {
	var dumped map[string]json.RawMessage
	if err := json.Unmarshal(raw, &dumped); err != nil {
		return nil, fmt.Errorf("decode config-key dump: %w", err)
	}
	records := make([]APIKeyRecord, 0, len(dumped))
	for key, val := range dumped {
		if !strings.HasPrefix(key, apiKeyConfigPrefix) {
			continue
		}
		var value string
		if err := json.Unmarshal(val, &value); err == nil {
			val = json.RawMessage([]byte(value))
		}
		rec, err := decodeAPIKeyRecord(strings.TrimPrefix(key, apiKeyConfigPrefix), val)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}
