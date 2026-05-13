package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/clyso/ceph-api/pkg/types"
)

const (
	apiKeyTokenPrefix = "capi_v1_"
	apiKeyIDPrefix    = "ak_"
	apiKeySecretSize  = 32
	apiKeyIDRandSize  = 16
)

type APIKeyRecord struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	ClusterID   string     `json:"cluster_id"`
	SecretHash  string     `json:"secret_hash"`
	Enabled     bool       `json:"enabled"`
	RevokedAt   *time.Time `json:"revoked_at"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   string     `json:"created_by"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
}

type parsedAPIKeyToken struct {
	ID     string
	Secret string
}

func newAPIKeyToken() (string, string, string, error) {
	idRaw := make([]byte, apiKeyIDRandSize)
	if _, err := rand.Read(idRaw); err != nil {
		return "", "", "", fmt.Errorf("generate API key id: %w", err)
	}
	secretRaw := make([]byte, apiKeySecretSize)
	if _, err := rand.Read(secretRaw); err != nil {
		return "", "", "", fmt.Errorf("generate API key secret: %w", err)
	}

	id := apiKeyIDPrefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(idRaw))
	secret := base64.RawURLEncoding.EncodeToString(secretRaw)
	return id, secret, apiKeyTokenPrefix + id + "." + secret, nil
}

func parseAPIKeyToken(token string) (parsedAPIKeyToken, error) {
	if !strings.HasPrefix(token, apiKeyTokenPrefix) {
		return parsedAPIKeyToken{}, types.ErrUnauthenticated
	}
	rest := strings.TrimPrefix(token, apiKeyTokenPrefix)
	id, secret, ok := strings.Cut(rest, ".")
	if !ok || !validAPIKeyID(id) || secret == "" || strings.Contains(secret, ".") {
		return parsedAPIKeyToken{}, types.ErrUnauthenticated
	}
	return parsedAPIKeyToken{ID: id, Secret: secret}, nil
}

func validAPIKeyID(id string) bool {
	return strings.HasPrefix(id, apiKeyIDPrefix) && !strings.Contains(id, "/") && len(id) > len(apiKeyIDPrefix)
}

func hashAPIKeySecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return "sha256:" + base64.StdEncoding.EncodeToString(sum[:])
}

func compareAPIKeySecret(secret, storedHash string) bool {
	computed := hashAPIKeySecret(secret)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}
