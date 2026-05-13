package auth

import (
	"context"
	"crypto/rsa"
	"fmt"
	"strings"

	xctx "github.com/clyso/ceph-api/pkg/ctx"
	"github.com/clyso/ceph-api/pkg/log"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/clyso/ceph-api/pkg/user"
	"github.com/golang-jwt/jwt"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"github.com/ory/fosite"
	"github.com/rs/zerolog"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func AuthFunc(userSvc *user.Service, provider fosite.OAuth2Provider, getKey func() *rsa.PublicKey, apiKeyStore *APIKeyStore) grpc_auth.AuthFunc {
	return func(ctx context.Context) (context.Context, error) {
		method, ok := grpc.Method(ctx)
		if !ok {
			return nil, fmt.Errorf("%w: unable to extract grpc method from context", types.ErrInternal)
		}
		// do not secure auth endpoits
		switch method {
		case "/ceph.Auth/Login", "/ceph.Auth/Check":
			return ctx, nil
		}

		tokenStr, err := grpc_auth.AuthFromMD(ctx, "bearer")
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("unable to extract bearer token from grpc meta")
			return nil, unauthenticated(fmt.Errorf("no token present: %w", types.ErrUnauthenticated))
		}
		if strings.HasPrefix(tokenStr, apiKeyTokenPrefix) {
			return authenticateAPIKey(ctx, tokenStr, apiKeyStore)
		}
		_, ar, err := provider.IntrospectToken(ctx, tokenStr, fosite.AccessToken, new(fosite.DefaultSession))
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("unable to introspect token")
			return nil, unauthenticated(fmt.Errorf("unable to introspect token: %w", types.ErrUnauthenticated))
		}
		username := ar.GetSession().GetSubject()
		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
			return getKey(), nil
		})
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("unable to parse jwt")
			return nil, unauthenticated(fmt.Errorf("unable to parse jwt: %w", types.ErrUnauthenticated))
		}
		if !token.Valid {
			zerolog.Ctx(ctx).Error().Msg("unable to validate jwt")
			return nil, unauthenticated(fmt.Errorf("unable to validate jwt: %w", types.ErrUnauthenticated))
		}
		if err = token.Claims.Valid(); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("unable to validate jwt claims")
			return nil, unauthenticated(fmt.Errorf("unable to validate jwt claims: %w", types.ErrUnauthenticated))
		}

		usr, err := userSvc.GetUser(ctx, username)
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Str("username", username).Msg("account not found")
			return nil, unauthenticated(types.ErrUnauthenticated)
		}
		ctx = log.WithUsername(ctx, usr.Username)
		ctx = xctx.SetRoles(ctx, usr.Roles)
		ctx = xctx.SetPermissions(ctx, userSvc.GetPermissions(ctx, username))
		ctx = xctx.SetAuthType(ctx, "jwt")
		ctx = xctx.SetAPIKeyID(ctx, "")

		return ctx, nil
	}
}

func unauthenticated(err error) error {
	code := codes.Unauthenticated
	info := &errdetails.ErrorInfo{
		Reason: "ErrUnauthenticated",
	}
	st, err := status.New(code, err.Error()).WithDetails(info)
	if err != nil {
		return status.Errorf(codes.Internal, "build grpc error: %s", err)
	}
	return st.Err()
}
