package auth

import (
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
)

const (
	tokenEndpoint  = "/api/oauth/token"
	revokeEndpoint = "/api/oauth/revoke"
	jwksURI        = "/.well-known/ceph-api/jwks.json"
)

type discoveryDocument struct {
	ClusterID   string        `json:"cluster_id"`
	ClusterName string        `json:"cluster_name"`
	Auth        discoveryAuth `json:"auth"`
	JWKSURI     string        `json:"jwks_uri"`
}

type discoveryAuth struct {
	Issuer             string   `json:"issuer"`
	Audience           string   `json:"audience"`
	TokenEndpoint      string   `json:"token_endpoint"`
	RevokeEndpoint     string   `json:"revoke_endpoint"`
	Modes              []string `json:"modes"`
	APIKeyPrefix       string   `json:"api_key_prefix,omitempty"`
	APIKeyHeaderFormat string   `json:"api_key_header_format,omitempty"`
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (s *Server) DiscoveryEndpoint(w http.ResponseWriter, r *http.Request) {
	modes := []string{"password"}
	auth := discoveryAuth{
		Issuer:         s.issuer,
		Audience:       s.clientID,
		TokenEndpoint:  tokenEndpoint,
		RevokeEndpoint: revokeEndpoint,
		Modes:          modes,
	}
	if s.apiKeyStore != nil {
		modes = append(modes, "api_key")
		auth.Modes = modes
		auth.APIKeyPrefix = apiKeyTokenPrefix
		auth.APIKeyHeaderFormat = "Authorization: Bearer capi_v1_<key_id>.<secret>"
	}
	writeJSON(w, discoveryDocument{
		Auth:    auth,
		JWKSURI: jwksURI,
	})
}

func (s *Server) JWKSEndpoint(w http.ResponseWriter, r *http.Request) {
	pub := s.publicKey
	w.Header().Set("Cache-Control", "max-age=300")
	writeJSON(w, jwksDocument{Keys: []jwk{{
		Kty: "RSA",
		Alg: "RS256",
		Use: "sig",
		Kid: s.keyID,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(new(big.Int).SetInt64(int64(pub.E)).Bytes()),
	}}})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
