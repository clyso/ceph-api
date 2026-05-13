package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	xctx "github.com/clyso/ceph-api/pkg/ctx"
)

func TestAPIKeyTokenHashAndParse(t *testing.T) {
	id, secret, token, err := newAPIKeyToken()
	if err != nil {
		t.Fatalf("newAPIKeyToken() error = %v", err)
	}
	if !strings.HasPrefix(token, apiKeyTokenPrefix+id+".") {
		t.Fatalf("token = %q, want prefix with id", token)
	}
	parsed, err := parseAPIKeyToken(token)
	if err != nil {
		t.Fatalf("parseAPIKeyToken() error = %v", err)
	}
	if parsed.ID != id || parsed.Secret != secret {
		t.Fatalf("parsed token = %+v, want id %q and original secret", parsed, id)
	}
	if !compareAPIKeySecret(secret, hashAPIKeySecret(secret)) {
		t.Fatal("compareAPIKeySecret() rejected matching secret")
	}
	if compareAPIKeySecret(secret+"x", hashAPIKeySecret(secret)) {
		t.Fatal("compareAPIKeySecret() accepted wrong secret")
	}
}

func TestAPIKeyStoreCreateListAndRevoke(t *testing.T) {
	ctx := context.Background()
	store := NewAPIKeyStore(newFakeMonCommander())
	now := time.Now().UTC()
	rec := APIKeyRecord{
		ID:         "ak_test",
		Name:       "test-key",
		Owner:      "user:admin",
		SecretHash: hashAPIKeySecret("secret"),
		Enabled:    true,
		CreatedAt:  now,
		CreatedBy:  "user:admin",
	}

	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != rec.ID || got.SecretHash != rec.SecretHash {
		t.Fatalf("Get() = %+v, want persisted record", got)
	}
	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != rec.ID {
		t.Fatalf("List() = %+v, want one record", listed)
	}
	if store.mon.(*fakeMonCommander).dumps != 1 {
		t.Fatalf("config-key dump count = %d, want 1", store.mon.(*fakeMonCommander).dumps)
	}
	revoked, err := store.Revoke(ctx, rec.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if revoked.Enabled || revoked.RevokedAt == nil {
		t.Fatalf("Revoke() = %+v, want disabled record with revoked_at", revoked)
	}
}

func TestAPIKeyStoreTouchLastUsedDebouncesWrites(t *testing.T) {
	ctx := context.Background()
	mon := newFakeMonCommander()
	store := NewAPIKeyStore(mon)
	now := time.Now().UTC()
	recent := now.Add(-apiKeyLastUsedDebounce / 2)
	rec := APIKeyRecord{
		ID:         "ak_test",
		Name:       "test-key",
		Owner:      "user:admin",
		SecretHash: hashAPIKeySecret("secret"),
		Enabled:    true,
		CreatedAt:  now,
		CreatedBy:  "user:admin",
		LastUsedAt: &recent,
	}
	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.TouchLastUsed(ctx, rec, now); err != nil {
		t.Fatalf("TouchLastUsed() recent error = %v", err)
	}
	if mon.sets != 1 {
		t.Fatalf("sets after recent touch = %d, want 1", mon.sets)
	}

	stale := now.Add(-apiKeyLastUsedDebounce * 2)
	rec.LastUsedAt = &stale
	if err := store.store(ctx, rec); err != nil {
		t.Fatalf("store stale record: %v", err)
	}
	if err := store.TouchLastUsed(ctx, rec, now); err != nil {
		t.Fatalf("TouchLastUsed() stale error = %v", err)
	}
	if mon.sets != 3 {
		t.Fatalf("sets after stale touch = %d, want 3", mon.sets)
	}
}

func TestAPIKeyStoreShouldTouchLastUsedDebouncesInMemory(t *testing.T) {
	store := NewAPIKeyStore(newFakeMonCommander())
	now := time.Now().UTC()
	stale := now.Add(-apiKeyLastUsedDebounce * 2)
	recent := now.Add(-apiKeyLastUsedDebounce / 2)

	if store.shouldTouchLastUsed(APIKeyRecord{ID: "ak_recent", LastUsedAt: &recent}, now) {
		t.Fatal("ShouldTouchLastUsed() with recent persisted timestamp = true, want false")
	}
	if !store.shouldTouchLastUsed(APIKeyRecord{ID: "ak_stale", LastUsedAt: &stale}, now) {
		t.Fatal("ShouldTouchLastUsed() first stale touch = false, want true")
	}
	if store.shouldTouchLastUsed(APIKeyRecord{ID: "ak_stale", LastUsedAt: &stale}, now.Add(time.Second)) {
		t.Fatal("ShouldTouchLastUsed() repeated stale touch = true, want false")
	}
}

func TestAuthenticateAPIKeySetsContextMetadata(t *testing.T) {
	ctx := context.Background()
	store := NewAPIKeyStore(newFakeMonCommander())
	id, secret, token, err := newAPIKeyToken()
	if err != nil {
		t.Fatalf("newAPIKeyToken() error = %v", err)
	}
	if err := store.Create(ctx, APIKeyRecord{
		ID:         id,
		Name:       "test-key",
		Owner:      "user:admin",
		SecretHash: hashAPIKeySecret(secret),
		Enabled:    true,
		CreatedAt:  time.Now().UTC(),
		CreatedBy:  "user:admin",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	gotCtx, err := authenticateAPIKey(ctx, token, store)
	if err != nil {
		t.Fatalf("authenticateAPIKey() error = %v", err)
	}
	if got := xctx.GetAuthType(gotCtx); got != "api_key" {
		t.Fatalf("auth type = %q, want api_key", got)
	}
	if got := xctx.GetAPIKeyID(gotCtx); got != id {
		t.Fatalf("api key id = %q, want %q", got, id)
	}
	if got := xctx.GetUsername(gotCtx); got != "apikey:"+id {
		t.Fatalf("username = %q, want apikey subject", got)
	}
	if len(xctx.GetPermissions(gotCtx)) == 0 {
		t.Fatal("permissions are empty, want administrator permissions")
	}
}

func TestServerAPIKeyCRUDRequiresJWTAdministrator(t *testing.T) {
	ctx := context.Background()
	ctx = xctx.SetUsername(ctx, "admin")
	ctx = xctx.SetAuthType(ctx, "jwt")
	ctx = xctx.SetRoles(ctx, []string{"administrator"})
	server := &Server{apiKeyStore: NewAPIKeyStore(newFakeMonCommander())}

	rec, token, err := server.CreateAPIKey(ctx, CreateAPIKeyRequest{Name: "terraform"})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if token == "" || rec.ID == "" || rec.SecretHash == "" {
		t.Fatalf("CreateAPIKey() returned incomplete key: rec=%+v token=%q", rec, token)
	}

	listed, err := server.ListAPIKeys(ctx)
	if err != nil {
		t.Fatalf("ListAPIKeys() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != rec.ID {
		t.Fatalf("ListAPIKeys() = %+v, want created key", listed)
	}
	if err := server.RevokeAPIKey(ctx, rec.ID); err != nil {
		t.Fatalf("RevokeAPIKey() error = %v", err)
	}
	revoked, err := server.GetAPIKey(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if revoked.Enabled || revoked.RevokedAt == nil {
		t.Fatalf("revoked key = %+v, want disabled with revoked_at", revoked)
	}

	apiKeyCtx := xctx.SetAuthType(ctx, "api_key")
	if _, _, err := server.CreateAPIKey(apiKeyCtx, CreateAPIKeyRequest{Name: "blocked"}); err == nil {
		t.Fatal("CreateAPIKey() with API-key auth succeeded, want access denied")
	}
}
