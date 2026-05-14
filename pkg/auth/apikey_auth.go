package auth

import (
	"context"
	"time"

	xctx "github.com/clyso/ceph-api/pkg/ctx"
	"github.com/clyso/ceph-api/pkg/log"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/clyso/ceph-api/pkg/user"
	"github.com/rs/zerolog"
)

const apiKeyLastUsedDebounce = time.Minute

func authenticateAPIKey(ctx context.Context, tokenStr string, store *APIKeyStore) (context.Context, error) {
	if store == nil {
		return nil, unauthenticated(types.ErrUnauthenticated)
	}
	parsed, err := parseAPIKeyToken(tokenStr)
	if err != nil {
		return nil, unauthenticated(err)
	}
	rec, err := store.Get(ctx, parsed.ID)
	if err != nil {
		zerolog.Ctx(ctx).Debug().Err(err).Str("api_key_id", parsed.ID).Msg("API key not found")
		return nil, unauthenticated(types.ErrUnauthenticated)
	}
	if !rec.Enabled || rec.RevokedAt != nil {
		return nil, unauthenticated(types.ErrUnauthenticated)
	}
	if rec.ExpiresAt != nil && time.Now().After(*rec.ExpiresAt) {
		return nil, unauthenticated(types.ErrUnauthenticated)
	}
	if !compareAPIKeySecret(parsed.Secret, rec.SecretHash) {
		return nil, unauthenticated(types.ErrUnauthenticated)
	}

	subject := "apikey:" + rec.ID
	ctx = log.WithUsername(ctx, subject)
	permissions, err := user.PermissionsFromInlineScopes(rec.Scopes)
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Str("api_key_id", rec.ID).Msg("API key has invalid scopes")
		return nil, unauthenticated(types.ErrUnauthenticated)
	}
	ctx = xctx.SetRoles(ctx, nil)
	ctx = xctx.SetPermissions(ctx, permissions)
	ctx = xctx.SetAuthType(ctx, "api_key")
	ctx = xctx.SetAPIKeyID(ctx, rec.ID)

	if store.shouldTouchLastUsed(rec, time.Now()) {
		go func(ctx context.Context, rec APIKeyRecord) {
			if err := store.TouchLastUsed(ctx, rec, time.Now().UTC()); err != nil {
				zerolog.Ctx(ctx).Debug().Err(err).Str("api_key_id", rec.ID).Msg("unable to update API key last-used metadata")
			}
		}(ctx, rec)
	}

	return ctx, nil
}
