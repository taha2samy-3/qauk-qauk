package authn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/keys"
)

// DeviceKey is what the verifier needs to know about a device and its key.
type DeviceKey struct {
	DeviceID   uuid.UUID
	DeviceName string
	KeyID      uuid.UUID
	PEM        string
	Algorithm  string
	KeyActive  bool
	// Public is the parsed PEM, if the loader already has it (else parsed here).
	Public any
}

// DeviceKeyLoader returns the device and its assigned key, or an error if the
// device doesn't exist or has no key.
type DeviceKeyLoader func(ctx context.Context, deviceID uuid.UUID) (*DeviceKey, error)

var ErrDeviceAuth = errors.New("device authentication failed")

type DeviceVerifier struct {
	Load        DeviceKeyLoader
	MaxLifetime time.Duration
	Leeway      time.Duration
	now         func() time.Time
}

// BearerToken extracts the token from an "Authorization: Bearer <t>" header value.
func BearerToken(header string) (string, bool) {
	scheme, tok, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || tok == "" {
		return "", false
	}
	return strings.TrimSpace(tok), true
}

// Verify authenticates a device JWT. The algorithm always comes from the
// stored key, never from the token header. Fixes B1 (inactive keys) and B2
// (exp required, lifetime capped).
func (v *DeviceVerifier) Verify(ctx context.Context, token string) (*DeviceKey, error) {
	var unverified jwt.MapClaims
	if _, _, err := jwt.NewParser().ParseUnverified(token, &unverified); err != nil {
		return nil, fmt.Errorf("%w: malformed token", ErrDeviceAuth)
	}
	rawID, _ := unverified["id"].(string)
	deviceID, err := uuid.Parse(rawID)
	if err != nil {
		return nil, fmt.Errorf("%w: missing or invalid id claim", ErrDeviceAuth)
	}
	dk, err := v.Load(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeviceAuth, err)
	}
	if !dk.KeyActive {
		return nil, fmt.Errorf("%w: key %s is inactive", ErrDeviceAuth, dk.KeyID)
	}
	pub := dk.Public
	if pub == nil {
		if pub, err = keys.ParsePublic(dk.PEM); err != nil {
			return nil, fmt.Errorf("%w: stored key unusable: %v", ErrDeviceAuth, err)
		}
	}
	now := time.Now
	if v.now != nil {
		now = v.now
	}
	var claims jwt.RegisteredClaims
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{dk.Algorithm}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(v.Leeway),
		jwt.WithTimeFunc(now),
	)
	if _, err := parser.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return pub, nil }); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeviceAuth, err)
	}
	if v.MaxLifetime > 0 {
		start := now()
		if claims.IssuedAt != nil {
			start = claims.IssuedAt.Time
		}
		if claims.ExpiresAt.Sub(start) > v.MaxLifetime+v.Leeway {
			return nil, fmt.Errorf("%w: token lifetime exceeds %s", ErrDeviceAuth, v.MaxLifetime)
		}
	}
	return dk, nil
}
