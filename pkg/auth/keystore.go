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
	priv, kid, err := k.load(ctx)
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
	if err := k.store(ctx, priv, kid); err != nil {
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

func (k *KeyStore) load(ctx context.Context) (*rsa.PrivateKey, string, error) {
	cmd, err := json.Marshal(map[string]string{"prefix": "config-key get", "key": keyJWTActive})
	if err != nil {
		return nil, "", err
	}
	raw, err := k.mon.ExecMon(ctx, string(cmd))
	if err != nil {
		return nil, "", err
	}

	var env keyStoreEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, "", fmt.Errorf("decode persisted JWT signing key envelope: %w", err)
	}
	if env.Version == 0 || len(env.Value) == 0 {
		return nil, "", fmt.Errorf("decode persisted JWT signing key envelope: missing value")
	}

	var rec persistedJWTKey
	if err := json.Unmarshal(env.Value, &rec); err != nil {
		return nil, "", fmt.Errorf("decode persisted JWT signing key: %w", err)
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

func (k *KeyStore) store(ctx context.Context, priv *rsa.PrivateKey, kid string) error {
	rec, err := json.Marshal(persistedJWTKey{
		KID:        kid,
		PrivateDER: base64.RawStdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(priv)),
	})
	if err != nil {
		return err
	}
	env, err := json.Marshal(keyStoreEnvelope{Version: 1, Value: json.RawMessage(rec)})
	if err != nil {
		return err
	}
	cmd, err := json.Marshal(map[string]string{"prefix": "config-key set", "key": keyJWTActive})
	if err != nil {
		return err
	}
	_, err = k.mon.ExecMonWithInputBuff(ctx, string(cmd), env)
	if err != nil {
		return fmt.Errorf("persist JWT signing key: %w", err)
	}
	return nil
}

func (k *KeyStore) loadGlobalSecret(ctx context.Context) ([]byte, error) {
	cmd, err := json.Marshal(map[string]string{"prefix": "config-key get", "key": keyGlobalSecretActive})
	if err != nil {
		return nil, err
	}
	raw, err := k.mon.ExecMon(ctx, string(cmd))
	if err != nil {
		return nil, err
	}

	var env keyStoreEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode persisted OAuth global secret envelope: %w", err)
	}
	if env.Version == 0 || len(env.Value) == 0 {
		return nil, fmt.Errorf("decode persisted OAuth global secret envelope: missing value")
	}

	var rec persistedGlobalSecret
	if err := json.Unmarshal(env.Value, &rec); err != nil {
		return nil, fmt.Errorf("decode persisted OAuth global secret: %w", err)
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
	rec, err := json.Marshal(persistedGlobalSecret{
		Secret: base64.RawStdEncoding.EncodeToString(secret),
	})
	if err != nil {
		return err
	}
	env, err := json.Marshal(keyStoreEnvelope{Version: 1, Value: json.RawMessage(rec)})
	if err != nil {
		return err
	}
	cmd, err := json.Marshal(map[string]string{"prefix": "config-key set", "key": keyGlobalSecretActive})
	if err != nil {
		return err
	}
	_, err = k.mon.ExecMonWithInputBuff(ctx, string(cmd), env)
	if err != nil {
		return fmt.Errorf("persist OAuth global secret: %w", err)
	}
	return nil
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
