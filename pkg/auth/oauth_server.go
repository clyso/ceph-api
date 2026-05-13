package auth

import (
	"context"
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/clyso/ceph-api/pkg/user"
	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	"github.com/ory/fosite/handler/oauth2"
	"github.com/ory/fosite/storage"
	"github.com/ory/fosite/token/jwt"
	"golang.org/x/crypto/bcrypt"
)

type Server struct {
	publicKey            *rsa.PublicKey
	keyID                string
	issuer               string
	clientID             string
	refreshTokenLifespan time.Duration

	provider fosite.OAuth2Provider
	storage  fosite.Storage

	authorizeCodeStrategy oauth2.AuthorizeCodeStrategy
	refreshTokenStrategy  oauth2.RefreshTokenStrategy
	accessTokenStrategy   oauth2.AccessTokenStrategy

	userSvc     *user.Service
	apiKeyStore *APIKeyStore
}

func NewServer(config Config, userSvc *user.Service, privateKey *rsa.PrivateKey, kid string, globalSecret []byte) (*Server, error) {
	if privateKey == nil {
		return nil, fmt.Errorf("JWT signing key is required")
	}
	if kid == "" {
		return nil, fmt.Errorf("JWT signing key id is required")
	}
	if len(globalSecret) < globalSecretSize {
		return nil, fmt.Errorf("OAuth global secret must be at least %d bytes", globalSecretSize)
	}
	secret := append([]byte(nil), globalSecret...)

	defaultStor := storage.NewMemoryStore()
	defaultStor.Clients = map[string]fosite.Client{
		config.ClientID: &fosite.DefaultClient{
			ID:            config.ClientID,
			Public:        true,
			ResponseTypes: []string{"id_token", "code", "token", "id_token token", "code id_token", "code token", "code id_token token"},
			GrantTypes:    []string{"refresh_token", "password"},
			Scopes:        []string{"openid", "offline"},
		}}

	storage := &fositeStore{
		userSvc:     userSvc,
		MemoryStore: defaultStor,
	}

	conf := &fosite.Config{
		GlobalSecret:  secret,
		ScopeStrategy: fosite.HierarchicScopeStrategy,
		// Allow all grants to get refresh token
		RefreshTokenScopes:   []string{},
		AccessTokenLifespan:  config.AccessTokenLifespan,
		RefreshTokenLifespan: config.RefreshTokenLifespan,
	}

	pGetter := func(ctx context.Context) (any, error) { return privateKey, nil }
	strategy := &compose.CommonStrategy{
		// Override default strategy to issue JWT instead of HMAC tokens
		CoreStrategy:               compose.NewOAuth2JWTStrategy(pGetter, compose.NewOAuth2HMACStrategy(conf), conf),
		OpenIDConnectTokenStrategy: compose.NewOpenIDConnectStrategy(pGetter, conf),
		Signer:                     &jwt.DefaultSigner{GetPrivateKey: pGetter}}

	oauth2Provider := compose.Compose(conf, storage, strategy,
		compose.OAuth2AuthorizeExplicitFactory,
		compose.OAuth2AuthorizeImplicitFactory,
		compose.OAuth2ClientCredentialsGrantFactory,
		compose.OAuth2RefreshTokenGrantFactory,
		compose.OAuth2ResourceOwnerPasswordCredentialsFactory,
		compose.RFC7523AssertionGrantFactory,
		compose.OAuth2TokenIntrospectionFactory,
		compose.OAuth2TokenRevocationFactory,
	)

	return &Server{
		publicKey:             &privateKey.PublicKey,
		keyID:                 kid,
		issuer:                config.Issuer,
		clientID:              config.ClientID,
		refreshTokenLifespan:  config.RefreshTokenLifespan,
		provider:              oauth2Provider,
		storage:               storage,
		authorizeCodeStrategy: strategy,
		refreshTokenStrategy:  strategy,
		accessTokenStrategy:   strategy,
		userSvc:               userSvc,
	}, nil
}

func (s *Server) Provider() fosite.OAuth2Provider {
	return s.provider
}

func (s *Server) GetPublicKey() *rsa.PublicKey {
	return s.publicKey
}

func (s *Server) newSession(subject string, claims map[string]interface{}) oauth2.JWTSessionContainer {
	jwtHeaders := make(map[string]interface{})
	jwtHeaders["kid"] = s.keyID
	return &oauth2.JWTSession{
		JWTClaims: &jwt.JWTClaims{
			Issuer:    s.issuer,
			Subject:   subject,
			ExpiresAt: time.Now().Add(time.Hour * 6),
			IssuedAt:  time.Now(),
			Extra:     claims,
		},
		JWTHeader: &jwt.Headers{
			Extra: jwtHeaders,
		},
	}
}

type fositeStore struct {
	userSvc *user.Service
	*storage.MemoryStore
}

func (s *fositeStore) Authenticate(ctx context.Context, name string, secret string) error {

	usr, err := s.userSvc.GetUser(ctx, name)
	if err != nil {
		return fosite.ErrNotFound
	}
	if !usr.Enabled {
		return fosite.ErrNotFound.WithDebug("User disabled")
	}
	err = bcrypt.CompareHashAndPassword([]byte(usr.Password), []byte(secret))
	if err != nil {
		return fosite.ErrNotFound.WithDebug("Invalid Credentials")
	}
	return nil
}
