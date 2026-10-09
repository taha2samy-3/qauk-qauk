package devtools

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestTransportFor(t *testing.T) {
	for _, c := range []struct {
		mode string
		i    int
		want string
	}{
		{"", 0, TransportWS}, {"ws", 2, TransportWS}, {TransportREST, 1, TransportREST}, {TransportGRPC, 0, TransportGRPC},
		{TransportMixed, 0, TransportWS}, {TransportMixed, 1, TransportREST}, {TransportMixed, 2, TransportGRPC}, {TransportMixed, 3, TransportWS},
	} {
		if got, err := transportFor(c.mode, c.i); err != nil || got != c.want {
			t.Errorf("transportFor(%q, %d) = %q, %v; want %q", c.mode, c.i, got, err, c.want)
		}
	}
	if _, err := transportFor("mqtt", 0); err == nil {
		t.Fatal("unknown transport must fail")
	}
}

func TestTokenForDemoDevice(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	file := filepath.Join(t.TempDir(), "demo.json")
	b, _ := json.Marshal(DemoFile{Devices: []DemoDevice{{ID: "01a1159f-d3a3-728e-85f6-2092dd7c17b7", Name: "Boiler room", Alg: "ES256",
		PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}}})
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"Boiler room", "01a1159f-d3a3-728e-85f6-2092dd7c17b7"} {
		tok, err := Token(file, ref, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		var claims jwt.MapClaims
		if _, err := jwt.ParseWithClaims(tok, &claims, func(*jwt.Token) (any, error) { return &k.PublicKey, nil },
			jwt.WithValidMethods([]string{"ES256"})); err != nil || claims["id"] != "01a1159f-d3a3-728e-85f6-2092dd7c17b7" {
			t.Fatalf("token for %q: %v %v", ref, claims, err)
		}
	}
	if _, err := Token(file, "nope", time.Hour); err == nil {
		t.Fatal("unknown device must fail")
	}
}
