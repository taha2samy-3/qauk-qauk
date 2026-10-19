package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taha2samy/quackquack/server/internal/authn"
)

func TestNewAPIToken(t *testing.T) {
	tok, hash, err := newAPIToken()
	if err != nil || !IsAPIToken(tok) || len(tok) != len(TokenPrefix)+43 || string(hash) != string(authn.HashToken(tok)) {
		t.Fatalf("%q %v", tok, err)
	}
	other, _, _ := newAPIToken()
	if other == tok {
		t.Fatal("tokens must be random")
	}
	for _, s := range []string{"", "qk_", "qk_short", "abc_" + strings.Repeat("A", 43)} {
		if IsAPIToken(s) {
			t.Errorf("%q isn't a token", s)
		}
	}
}

// Bad input is refused before any database work.
func TestCreateAPITokenValidation(t *testing.T) {
	s := &Service{}
	past := time.Now().Add(-time.Hour)
	for name, in := range map[string]TokenInput{
		"no name":       {Scopes: []string{"read"}},
		"no scope":      {Name: "x"},
		"unknown scope": {Name: "x", Scopes: []string{"admin"}},
		"expired":       {Name: "x", Scopes: []string{"read"}, ExpiresAt: &past},
	} {
		if _, _, err := s.CreateAPIToken(context.Background(), SystemActor, 1, in); err == nil {
			t.Errorf("%s: must be refused", name)
		} else if _, ok := IsValidation(err); !ok {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}
}
