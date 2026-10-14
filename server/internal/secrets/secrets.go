// Package secrets stores secret settings (webhook URLs with tokens, signing
// secrets, header values) without keeping them readable in the database.
//
// A stored value is one of:
//   - a reference, env:NAME or file:/path, resolved where it is used and
//     never stored resolved (the platform's preferred form, as for MQTT);
//   - "enc:v1:" + base64(nonce ‖ AES-256-GCM ciphertext), with the key from
//     QUACK_SECRETS_KEY;
//   - plain text, when no key is configured (a startup warning says so) or
//     for rows written before encryption.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/taha2samy/quackquack/server/internal/mqttspec"
)

const sealedPrefix = "enc:v1:"

// Box seals and opens stored secrets. A nil key (empty QUACK_SECRETS_KEY)
// stores literal values as plain text.
type Box struct {
	aead cipher.AEAD
}

// NewBox builds a box from a base64-encoded 32-byte key ("" = no encryption).
func NewBox(keyB64 string) (*Box, error) {
	keyB64 = strings.TrimSpace(keyB64)
	if keyB64 == "" {
		return &Box{}, nil
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		return nil, errors.New("QUACK_SECRETS_KEY must be the base64 encoding of 32 random bytes (openssl rand -base64 32)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypts reports whether literal values are stored encrypted.
func (b *Box) Encrypts() bool { return b != nil && b.aead != nil }

// IsRef reports whether v is an env: or file: reference.
func IsRef(v string) bool { return mqttspec.IsRef(v) }

// Seal turns a value from the API into its stored form: references and ""
// stay as they are, literal values are encrypted when a key is configured.
func (b *Box) Seal(v string) (string, error) {
	if v == "" || IsRef(v) || !b.Encrypts() {
		return v, nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return sealedPrefix + base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(v), nil)), nil
}

// Open returns the usable value of a stored one: references are resolved,
// sealed values decrypted, plain text returned as is.
func (b *Box) Open(stored string) (string, error) {
	switch {
	case stored == "":
		return "", nil
	case IsRef(stored):
		v, err := mqttspec.Resolve(stored)
		return strings.TrimSpace(string(v)), err
	case strings.HasPrefix(stored, sealedPrefix):
		if !b.Encrypts() {
			return "", errors.New("the value is encrypted but QUACK_SECRETS_KEY is not set")
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, sealedPrefix))
		n := b.aead.NonceSize()
		if err != nil || len(raw) < n {
			return "", errors.New("malformed encrypted value")
		}
		plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
		if err != nil {
			return "", fmt.Errorf("can't decrypt (was QUACK_SECRETS_KEY changed?): %w", err)
		}
		return string(plain), nil
	}
	return stored, nil
}
