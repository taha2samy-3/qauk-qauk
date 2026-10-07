package keys

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func pemOf(t *testing.T, pub crypto.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestAnalyze(t *testing.T) {
	r2048, _ := rsa.GenerateKey(rand.Reader, 2048)
	r1024, _ := rsa.GenerateKey(rand.Reader, 1024)
	p256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	edPub, _, _ := ed25519.GenerateKey(rand.Reader)

	if info, err := Analyze(pemOf(t, &r2048.PublicKey)); err != nil || info.Algorithm != "RS256" || info.KeySize != 2048 {
		t.Fatalf("rsa2048: %+v %v", info, err)
	}
	if info, err := Analyze(pemOf(t, &p256.PublicKey)); err != nil || info.Algorithm != "ES256" || info.KeySize != 256 {
		t.Fatalf("p256: %+v %v", info, err)
	}
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&r2048.PublicKey)}))
	if _, err := Analyze(pkcs1); err != nil {
		t.Fatalf("pkcs1 rsa: %v", err)
	}
	for name, p := range map[string]string{
		"rsa1024": pemOf(t, &r1024.PublicKey), "p384": pemOf(t, &p384.PublicKey), "ed25519": pemOf(t, edPub),
		"garbage": "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n", "empty": "",
	} {
		if _, err := Analyze(p); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
