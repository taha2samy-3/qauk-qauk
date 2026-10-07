// Package authn implements user and device authentication.
package authn

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (OWASP 2024 minimum: m=19 MiB, t=2, p=1).
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

var ErrUnknownHashFormat = errors.New("authn: unknown password hash format")

// HashPassword returns a PHC-formatted argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks password against an argon2id hash or a Django
// pbkdf2_sha256 hash. needsRehash reports that the stored hash should be
// replaced with a fresh argon2id hash.
func VerifyPassword(encoded, password string) (ok, needsRehash bool, err error) {
	switch {
	case strings.HasPrefix(encoded, "$argon2id$"):
		ok, err = verifyArgon2id(encoded, password)
		return ok, false, err
	case strings.HasPrefix(encoded, "pbkdf2_sha256$"):
		ok, err = verifyDjangoPBKDF2(encoded, password)
		return ok, ok, err
	default:
		return false, false, ErrUnknownHashFormat
	}
}

func verifyArgon2id(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return false, ErrUnknownHashFormat
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, ErrUnknownHashFormat
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, ErrUnknownHashFormat
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, ErrUnknownHashFormat
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// verifyDjangoPBKDF2 verifies Django's "pbkdf2_sha256$<iterations>$<salt>$<b64 hash>".
func verifyDjangoPBKDF2(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 {
		return false, ErrUnknownHashFormat
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false, ErrUnknownHashFormat
	}
	want, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, ErrUnknownHashFormat
	}
	got, err := pbkdf2.Key(sha256.New, password, []byte(parts[2]), iter, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// DummyVerify burns comparable CPU time when a user doesn't exist, so login
// timing does not reveal valid usernames.
func DummyVerify(password string) {
	_, _ = verifyArgon2id(dummyHash, password)
}

var dummyHash, _ = HashPassword("dummy-password-for-timing")
