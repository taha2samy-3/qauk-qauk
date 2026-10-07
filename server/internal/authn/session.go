package authn

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const (
	SessionCookie = "quack_session"
	tokenBytes    = 32
)

// NewSessionToken returns a random token for the cookie and the SHA-256 hash
// stored in the database. The raw token is never persisted.
func NewSessionToken() (token string, hash []byte, err error) {
	b := make([]byte, tokenBytes)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
