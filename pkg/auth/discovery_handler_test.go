package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoveryEndpoint(t *testing.T) {
	server := newTestServer(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/ceph-api", nil)

	server.DiscoveryEndpoint(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got, want := recorder.Header().Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}

	var doc discoveryDocument
	if err := json.Unmarshal(recorder.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if doc.Auth.Issuer != "http://issuer.example" {
		t.Fatalf("issuer = %q", doc.Auth.Issuer)
	}
	if doc.Auth.Audience != "ceph-api" {
		t.Fatalf("audience = %q", doc.Auth.Audience)
	}
	if doc.Auth.TokenEndpoint != tokenEndpoint {
		t.Fatalf("token endpoint = %q", doc.Auth.TokenEndpoint)
	}
	if doc.Auth.RevokeEndpoint != revokeEndpoint {
		t.Fatalf("revoke endpoint = %q", doc.Auth.RevokeEndpoint)
	}
	if doc.JWKSURI != jwksURI {
		t.Fatalf("jwks uri = %q", doc.JWKSURI)
	}
	if len(doc.Auth.Modes) != 1 || doc.Auth.Modes[0] != "password" {
		t.Fatalf("modes = %v", doc.Auth.Modes)
	}
	if doc.Auth.APIKeyPrefix != "" || doc.Auth.APIKeyHeaderFormat != "" {
		t.Fatalf("api key fields = %q %q, want empty without API-key store", doc.Auth.APIKeyPrefix, doc.Auth.APIKeyHeaderFormat)
	}
}

func TestDiscoveryEndpointAdvertisesAPIKeyFormat(t *testing.T) {
	server := newTestServer(t)
	server.apiKeyStore = NewAPIKeyStore(newFakeMonCommander())
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/ceph-api", nil)

	server.DiscoveryEndpoint(recorder, req)

	var doc discoveryDocument
	if err := json.Unmarshal(recorder.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(doc.Auth.Modes) != 2 || doc.Auth.Modes[0] != "password" || doc.Auth.Modes[1] != "api_key" {
		t.Fatalf("modes = %v", doc.Auth.Modes)
	}
	if doc.Auth.APIKeyPrefix != apiKeyTokenPrefix {
		t.Fatalf("api key prefix = %q, want %q", doc.Auth.APIKeyPrefix, apiKeyTokenPrefix)
	}
	if doc.Auth.APIKeyHeaderFormat != "Authorization: Bearer capi_v1_<key_id>.<secret>" {
		t.Fatalf("api key header format = %q", doc.Auth.APIKeyHeaderFormat)
	}
}

func TestJWKSEndpoint(t *testing.T) {
	server := newTestServer(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/ceph-api/jwks.json", nil)

	server.JWKSEndpoint(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got, want := recorder.Header().Get("Cache-Control"), "max-age=300"; got != want {
		t.Fatalf("Cache-Control = %q, want %q", got, want)
	}
	if got, want := recorder.Header().Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}

	var doc jwksDocument
	if err := json.Unmarshal(recorder.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("keys len = %d, want 1", len(doc.Keys))
	}
	key := doc.Keys[0]
	if key.Kty != "RSA" || key.Alg != "RS256" || key.Use != "sig" {
		t.Fatalf("unexpected key metadata: %+v", key)
	}
	if key.Kid != server.keyID {
		t.Fatalf("kid = %q, want %q", key.Kid, server.keyID)
	}
	if key.N == "" || key.E == "" {
		t.Fatalf("empty key material: %+v", key)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	kid, err := computeKID(&priv.PublicKey)
	if err != nil {
		t.Fatalf("compute kid: %v", err)
	}
	globalSecret := []byte("0123456789abcdef0123456789abcdef")
	server, err := NewServer(Config{ClientID: "ceph-api", Issuer: "http://issuer.example"}, nil, priv, kid, nil, globalSecret)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}
