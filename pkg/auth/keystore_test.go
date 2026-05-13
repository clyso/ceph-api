package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/clyso/ceph-api/pkg/types"
)

func TestKeyStoreLoadOrCreateCreatesAndReloadsKey(t *testing.T) {
	ctx := context.Background()
	mon := newFakeMonCommander()
	store := NewKeyStore(mon)

	priv, kid, err := store.LoadOrCreate(ctx)
	if err != nil {
		t.Fatalf("LoadOrCreate() error = %v", err)
	}
	if priv == nil {
		t.Fatal("LoadOrCreate() private key is nil")
	}
	if kid == "" {
		t.Fatal("LoadOrCreate() kid is empty")
	}
	if mon.sets != 1 {
		t.Fatalf("config-key set count = %d, want 1", mon.sets)
	}

	reloadedPriv, reloadedKID, err := store.LoadOrCreate(ctx)
	if err != nil {
		t.Fatalf("second LoadOrCreate() error = %v", err)
	}
	if reloadedKID != kid {
		t.Fatalf("reloaded kid = %q, want %q", reloadedKID, kid)
	}
	if reloadedPriv.N.Cmp(priv.N) != 0 || reloadedPriv.E != priv.E || reloadedPriv.D.Cmp(priv.D) != 0 {
		t.Fatal("reloaded private key does not match persisted key")
	}
	if mon.sets != 1 {
		t.Fatalf("config-key set count after reload = %d, want 1", mon.sets)
	}
}

func TestNewServerUsesProvidedKID(t *testing.T) {
	ctx := context.Background()
	store := NewKeyStore(newFakeMonCommander())
	priv, kid, err := store.LoadOrCreate(ctx)
	if err != nil {
		t.Fatalf("LoadOrCreate() error = %v", err)
	}

	server, err := NewServer(Config{ClientID: "ceph-api", Issuer: "test"}, nil, priv, kid)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	jwtSession := server.newSession("admin", nil)
	got, _ := jwtSession.GetJWTHeader().Extra["kid"].(string)
	if got != kid {
		t.Fatalf("JWT header kid = %q, want %q", got, kid)
	}
}

type fakeMonCommander struct {
	values map[string][]byte
	sets   int
}

func newFakeMonCommander() *fakeMonCommander {
	return &fakeMonCommander{values: map[string][]byte{}}
}

func (f *fakeMonCommander) ExecMon(_ context.Context, cmd string) ([]byte, error) {
	var req struct {
		Prefix string `json:"prefix"`
		Key    string `json:"key"`
	}
	if err := json.Unmarshal([]byte(cmd), &req); err != nil {
		return nil, err
	}
	if req.Prefix != "config-key get" {
		return nil, errors.New("unexpected ExecMon command")
	}
	value, ok := f.values[req.Key]
	if !ok {
		return nil, types.RadosErrorNotFound
	}
	return append([]byte(nil), value...), nil
}

func (f *fakeMonCommander) ExecMonWithInputBuff(_ context.Context, cmd string, in []byte) ([]byte, error) {
	var req struct {
		Prefix string `json:"prefix"`
		Key    string `json:"key"`
	}
	if err := json.Unmarshal([]byte(cmd), &req); err != nil {
		return nil, err
	}
	if req.Prefix != "config-key set" {
		return nil, errors.New("unexpected ExecMonWithInputBuff command")
	}
	f.values[req.Key] = append([]byte(nil), in...)
	f.sets++
	return nil, nil
}
