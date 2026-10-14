package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func key(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func TestSealOpen(t *testing.T) {
	b, err := NewBox(key(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal("https://hooks.slack.com/services/T0/B0/secret")
	if err != nil || !strings.HasPrefix(sealed, sealedPrefix) || strings.Contains(sealed, "secret") {
		t.Fatalf("sealed %q %v", sealed, err)
	}
	if again, _ := b.Seal("https://hooks.slack.com/services/T0/B0/secret"); again == sealed {
		t.Fatal("each seal needs a fresh nonce")
	}
	if got, err := b.Open(sealed); err != nil || got != "https://hooks.slack.com/services/T0/B0/secret" {
		t.Fatalf("open: %q %v", got, err)
	}
	other, _ := NewBox(key(t))
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("another key must not open it")
	}
	if _, err := (&Box{}).Open(sealed); err == nil {
		t.Fatal("no key: an error, not the ciphertext")
	}
}

func TestReferencesAndPlainText(t *testing.T) {
	b, _ := NewBox(key(t))
	t.Setenv("QUACK_TEST_HOOK", " https://example.com/x \n")
	if s, _ := b.Seal("env:QUACK_TEST_HOOK"); s != "env:QUACK_TEST_HOOK" {
		t.Fatalf("references are stored as written, got %q", s)
	}
	if v, err := b.Open("env:QUACK_TEST_HOOK"); err != nil || v != "https://example.com/x" {
		t.Fatalf("%q %v", v, err)
	}
	f := filepath.Join(t.TempDir(), "s")
	_ = os.WriteFile(f, []byte("whsec_abc\n"), 0o600)
	if v, err := b.Open("file:" + f); err != nil || v != "whsec_abc" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := b.Open("env:QUACK_TEST_MISSING"); err == nil {
		t.Fatal("a missing variable is an error")
	}
	// no key: literal values are kept as they are (legacy rows too)
	plain, _ := NewBox("")
	if s, _ := plain.Seal("abc"); s != "abc" || plain.Encrypts() {
		t.Fatal(s)
	}
	if v, _ := b.Open("legacy-plain"); v != "legacy-plain" {
		t.Fatal(v)
	}
	if _, err := NewBox("short"); err == nil {
		t.Fatal("a bad key must be refused")
	}
}
