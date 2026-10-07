// Package keys validates device public keys (port of JWTPublicKey._analyze_and_populate_key_details).
package keys

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

const minRSABits = 2048

type Info struct {
	Algorithm string // RS256 or ES256
	KeySize   int
}

// Analyze parses a PEM public key and returns the JWT algorithm it supports.
// Only RSA (>= 2048 bits) and ECDSA P-256 keys are accepted.
func Analyze(pemText string) (Info, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return Info{}, errors.New("invalid PEM: no PEM block found")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		if rsaKey, rerr := x509.ParsePKCS1PublicKey(block.Bytes); rerr == nil {
			pub = rsaKey
		} else {
			return Info{}, fmt.Errorf("invalid PEM public key: %w", err)
		}
	}
	return analyzeKey(pub)
}

func analyzeKey(pub any) (Info, error) {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		bits := k.N.BitLen()
		if bits < minRSABits {
			return Info{}, fmt.Errorf("RSA key too small: %d bits (minimum %d)", bits, minRSABits)
		}
		return Info{Algorithm: "RS256", KeySize: bits}, nil
	case *ecdsa.PublicKey:
		if k.Curve != elliptic.P256() {
			return Info{}, fmt.Errorf("unsupported EC curve %q: only P-256 (ES256) is supported", k.Curve.Params().Name)
		}
		return Info{Algorithm: "ES256", KeySize: 256}, nil
	default:
		return Info{}, errors.New("unsupported key type: only RSA and ECDSA keys are supported")
	}
}

// ParsePublic returns the crypto public key for signature verification.
func ParsePublic(pemText string) (any, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("invalid PEM")
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return pub, nil
	}
	return x509.ParsePKCS1PublicKey(block.Bytes)
}
