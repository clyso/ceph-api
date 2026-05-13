package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/clyso/ceph-api/pkg/types"
)

const (
	keyJWTActive          = "ceph-api/auth/jwt-key/active"
	keyGlobalSecretActive = "ceph-api/auth/global-secret/active"
	globalSecretSize      = 32
)

type MonCommander interface {
	ExecMon(ctx context.Context, cmd string) ([]byte, error)
	ExecMonWithInputBuff(ctx context.Context, cmd string, in []byte) ([]byte, error)
}

type KeyStore struct {
	mon MonCommander
}

type persistedJWTKey struct {
	KID        string `json:"kid"`
	PrivateDER string `json:"private_der"`
}

type persistedGlobalSecret struct {
	Secret string `json:"secret"`
}

type keyStoreEnvelope struct {
	Version uint64          `json:"version"`
	Value   json.RawMessage `json:"value"`
}

func NewKeyStore(mon MonCommander) *KeyStore {
	return &KeyStore{mon: mon}
}

func (k *KeyStore) LoadOrCreate(ctx context.Context) (*rsa.PrivateKey, string, error) {
	priv, kid, err := k.loadJWTKey(ctx)
	if err == nil {
		return priv, kid, nil
	}
	if !errors.Is(err, types.RadosErrorNotFound) {
		return nil, "", err
	}

	priv, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", fmt.Errorf("generate JWT signing key: %w", err)
	}
	kid, err = computeKID(&priv.PublicKey)
	if err != nil {
		return nil, "", err
	}
	// TODO: Re-load after first persist to detect concurrent starters racing on config-key set.
	if err := k.storeJWTKey(ctx, priv, kid); err != nil {
		return nil, "", err
	}
	return priv, kid, nil
}

func (k *KeyStore) LoadOrCreateGlobalSecret(ctx context.Context) ([]byte, error) {
	secret, err := k.loadGlobalSecret(ctx)
	if err == nil {
		return secret, nil
	}
	if !errors.Is(err, types.RadosErrorNotFound) {
		return nil, err
	}

	secret = make([]byte, globalSecretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate OAuth global secret: %w", err)
	}
	// TODO: Re-load after first persist to detect concurrent starters racing on config-key set.
	if err := k.storeGlobalSecret(ctx, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// storeEnvelope marshals value into the versioned keyStoreEnvelope and writes
// it to the given config-key. label is only used to contextualize errors.
func storeEnvelope(ctx context.Context, mon MonCommander, key string, value any, label string) error {
	rec, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", label, err)
	}
	env, err := json.Marshal(keyStoreEnvelope{Version: 1, Value: json.RawMessage(rec)})
	if err != nil {
		return fmt.Errorf("encode %s envelope: %w", label, err)
	}
	cmd, err := json.Marshal(map[string]string{"prefix": "config-key set", "key": key})
	if err != nil {
		return err
	}
	if _, err := mon.ExecMonWithInputBuff(ctx, string(cmd), env); err != nil {
		return fmt.Errorf("persist %s: %w", label, err)
	}
	return nil
}

func loadEnvelope(ctx context.Context, mon MonCommander, key string, out any, label string) error {
	cmd, err := json.Marshal(map[string]string{"prefix": "config-key get", "key": key})
	if err != nil {
		return err
	}
	raw, err := mon.ExecMon(ctx, string(cmd))
	if err != nil {
		return err
	}
	return decodeEnvelope(raw, out, label)
}

func decodeEnvelope(raw []byte, out any, label string) error {
	var env keyStoreEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode persisted %s envelope: %w", label, err)
	}
	if env.Version == 0 || len(env.Value) == 0 {
		return fmt.Errorf("decode persisted %s envelope: missing value", label)
	}
	if err := json.Unmarshal(env.Value, out); err != nil {
		return fmt.Errorf("decode persisted %s: %w", label, err)
	}
	return nil
}

func (k *KeyStore) loadJWTKey(ctx context.Context) (*rsa.PrivateKey, string, error) {
	var rec persistedJWTKey
	if err := loadEnvelope(ctx, k.mon, keyJWTActive, &rec, "JWT signing key"); err != nil {
		return nil, "", err
	}
	der, err := base64.RawStdEncoding.DecodeString(rec.PrivateDER)
	if err != nil {
		return nil, "", fmt.Errorf("decode persisted JWT signing key DER: %w", err)
	}
	priv, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, "", fmt.Errorf("parse persisted JWT signing key: %w", err)
	}
	kid, err := computeKID(&priv.PublicKey)
	if err != nil {
		return nil, "", err
	}
	if rec.KID != "" && rec.KID != kid {
		return nil, "", fmt.Errorf("persisted JWT signing key kid mismatch")
	}
	return priv, kid, nil
}

func (k *KeyStore) storeJWTKey(ctx context.Context, priv *rsa.PrivateKey, kid string) error {
	return storeEnvelope(ctx, k.mon, keyJWTActive, persistedJWTKey{
		KID:        kid,
		PrivateDER: base64.RawStdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(priv)),
	}, "JWT signing key")
}

func (k *KeyStore) loadGlobalSecret(ctx context.Context) ([]byte, error) {
	var rec persistedGlobalSecret
	if err := loadEnvelope(ctx, k.mon, keyGlobalSecretActive, &rec, "OAuth global secret"); err != nil {
		return nil, err
	}
	secret, err := base64.RawStdEncoding.DecodeString(rec.Secret)
	if err != nil {
		return nil, fmt.Errorf("decode persisted OAuth global secret value: %w", err)
	}
	if len(secret) != globalSecretSize {
		return nil, fmt.Errorf("decode persisted OAuth global secret: invalid size")
	}
	return secret, nil
}

func (k *KeyStore) storeGlobalSecret(ctx context.Context, secret []byte) error {
	return storeEnvelope(ctx, k.mon, keyGlobalSecretActive, persistedGlobalSecret{
		Secret: base64.RawStdEncoding.EncodeToString(secret),
	}, "OAuth global secret")
}

func computeKID(pub *rsa.PublicKey) (string, error) {
	h := sha256.New()
	if _, err := h.Write(pub.N.Bytes()); err != nil {
		return "", err
	}
	if err := binary.Write(h, binary.BigEndian, int64(pub.E)); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:16]), nil
}
