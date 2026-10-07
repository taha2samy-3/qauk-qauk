package authn

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func pubPEM(t *testing.T, k crypto.Signer) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(k.Public())
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

type fixture struct {
	dev     uuid.UUID
	rsaKey  *rsa.PrivateKey
	ecKey   *ecdsa.PrivateKey
	stored  DeviceKey
	v       *DeviceVerifier
	missing bool
}

func newFixture(t *testing.T) *fixture {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	f := &fixture{dev: uuid.New(), rsaKey: rk, ecKey: ek}
	f.stored = DeviceKey{DeviceID: f.dev, DeviceName: "d", KeyID: uuid.New(), PEM: pubPEM(t, rk), Algorithm: "RS256", KeyActive: true}
	f.v = &DeviceVerifier{MaxLifetime: 24 * time.Hour, Leeway: 30 * time.Second, Load: func(_ context.Context, id uuid.UUID) (*DeviceKey, error) {
		if f.missing || id != f.dev {
			return nil, errors.New("not found")
		}
		k := f.stored
		return &k, nil
	}}
	return f
}

func sign(t *testing.T, m jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(m, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDeviceVerify(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	good := jwt.MapClaims{"id": f.dev.String(), "exp": now.Add(time.Hour).Unix()}

	if _, err := f.v.Verify(context.Background(), sign(t, jwt.SigningMethodRS256, f.rsaKey, good)); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}

	otherRSA, _ := rsa.GenerateKey(rand.Reader, 2048)
	cases := map[string]string{
		"bad signature":      sign(t, jwt.SigningMethodRS256, otherRSA, good),
		"alg mismatch ES256": sign(t, jwt.SigningMethodES256, f.ecKey, good),
		"alg none":           sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, good),
		"HS256 with pem as secret (alg confusion)": sign(t, jwt.SigningMethodHS256, []byte(f.stored.PEM), good),
		"B2 missing exp":    sign(t, jwt.SigningMethodRS256, f.rsaKey, jwt.MapClaims{"id": f.dev.String()}),
		"expired":           sign(t, jwt.SigningMethodRS256, f.rsaKey, jwt.MapClaims{"id": f.dev.String(), "exp": now.Add(-time.Hour).Unix()}),
		"lifetime too long": sign(t, jwt.SigningMethodRS256, f.rsaKey, jwt.MapClaims{"id": f.dev.String(), "exp": now.Add(30 * 24 * time.Hour).Unix()}),
		"missing id":        sign(t, jwt.SigningMethodRS256, f.rsaKey, jwt.MapClaims{"exp": now.Add(time.Hour).Unix()}),
		"unknown device":    sign(t, jwt.SigningMethodRS256, f.rsaKey, jwt.MapClaims{"id": uuid.NewString(), "exp": now.Add(time.Hour).Unix()}),
		"garbage":           "not.a.jwt",
	}
	for name, tok := range cases {
		if _, err := f.v.Verify(context.Background(), tok); !errors.Is(err, ErrDeviceAuth) {
			t.Errorf("%s: want ErrDeviceAuth, got %v", name, err)
		}
	}

	f.stored.KeyActive = false // B1
	if _, err := f.v.Verify(context.Background(), sign(t, jwt.SigningMethodRS256, f.rsaKey, good)); !errors.Is(err, ErrDeviceAuth) {
		t.Errorf("B1 inactive key accepted: %v", err)
	}
}

func TestDeviceVerifyES256(t *testing.T) {
	f := newFixture(t)
	f.stored.PEM, f.stored.Algorithm = pubPEM(t, f.ecKey), "ES256"
	tok := sign(t, jwt.SigningMethodES256, f.ecKey, jwt.MapClaims{"id": f.dev.String(), "exp": time.Now().Add(time.Hour).Unix()})
	if _, err := f.v.Verify(context.Background(), tok); err != nil {
		t.Fatalf("ES256 rejected: %v", err)
	}
}

func TestBearerToken(t *testing.T) {
	for in, want := range map[string]string{"Bearer abc": "abc", "bearer  abc ": "abc", "Basic abc": "", "Bearer": "", "": ""} {
		got, ok := BearerToken(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q -> %q,%v", in, got, ok)
		}
	}
}
