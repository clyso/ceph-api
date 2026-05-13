package auth

import (
	"context"
	"fmt"
	"time"

	xctx "github.com/clyso/ceph-api/pkg/ctx"
	"github.com/clyso/ceph-api/pkg/types"
)

type CreateAPIKeyRequest struct {
	Name        string
	Description string
	ExpiresAt   *time.Time
}

func (s *Server) SetAPIKeyStore(store *APIKeyStore) {
	s.apiKeyStore = store
}

func (s *Server) CreateAPIKey(ctx context.Context, req CreateAPIKeyRequest) (APIKeyRecord, string, error) {
	if err := requireJWTAdministrator(ctx); err != nil {
		return APIKeyRecord{}, "", err
	}
	if s.apiKeyStore == nil {
		return APIKeyRecord{}, "", fmt.Errorf("%w: API key store is not configured", types.ErrInternal)
	}
	if req.Name == "" {
		return APIKeyRecord{}, "", fmt.Errorf("%w: API key name is required", types.ErrInvalidArg)
	}
	if req.ExpiresAt != nil && req.ExpiresAt.Before(time.Now()) {
		return APIKeyRecord{}, "", fmt.Errorf("%w: API key expiration must be in the future", types.ErrInvalidArg)
	}

	id, secret, token, err := newAPIKeyToken()
	if err != nil {
		return APIKeyRecord{}, "", err
	}
	now := time.Now().UTC()
	username := xctx.GetUsername(ctx)
	// API-key v1 intentionally issues administrator keys only. Scoped roles are
	// deferred until role grants can be persisted and enforced end-to-end.
	rec := APIKeyRecord{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Owner:       "user:" + username,
		SecretHash:  hashAPIKeySecret(secret),
		Enabled:     true,
		CreatedAt:   now,
		CreatedBy:   "user:" + username,
		ExpiresAt:   req.ExpiresAt,
	}
	if err := s.apiKeyStore.Create(ctx, rec); err != nil {
		return APIKeyRecord{}, "", err
	}
	return rec, token, nil
}

func (s *Server) ListAPIKeys(ctx context.Context) ([]APIKeyRecord, error) {
	if err := requireJWTAdministrator(ctx); err != nil {
		return nil, err
	}
	if s.apiKeyStore == nil {
		return nil, fmt.Errorf("%w: API key store is not configured", types.ErrInternal)
	}
	return s.apiKeyStore.List(ctx)
}

func (s *Server) GetAPIKey(ctx context.Context, id string) (APIKeyRecord, error) {
	if err := requireJWTAdministrator(ctx); err != nil {
		return APIKeyRecord{}, err
	}
	if s.apiKeyStore == nil {
		return APIKeyRecord{}, fmt.Errorf("%w: API key store is not configured", types.ErrInternal)
	}
	return s.apiKeyStore.Get(ctx, id)
}

func (s *Server) RevokeAPIKey(ctx context.Context, id string) error {
	if err := requireJWTAdministrator(ctx); err != nil {
		return err
	}
	if s.apiKeyStore == nil {
		return fmt.Errorf("%w: API key store is not configured", types.ErrInternal)
	}
	_, err := s.apiKeyStore.Revoke(ctx, id, time.Now().UTC())
	return err
}

func requireJWTAdministrator(ctx context.Context) error {
	if xctx.GetUsername(ctx) == "" {
		return types.ErrUnauthenticated
	}
	if xctx.GetAuthType(ctx) != "jwt" {
		return types.ErrAccessDenied
	}
	for _, role := range xctx.GetRoles(ctx) {
		if role == "administrator" {
			return nil
		}
	}
	return types.ErrAccessDenied
}
