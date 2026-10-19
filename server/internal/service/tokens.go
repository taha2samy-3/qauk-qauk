package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// TokenPrefix starts every API token, so it is recognizable (secret scanners,
// logs) and told apart from session cookies.
const TokenPrefix = "qk_"

// MaxTokensPerUser bounds a machine user's tokens.
const MaxTokensPerUser = 20

// IsAPIToken reports whether s looks like an API token.
func IsAPIToken(s string) bool { return strings.HasPrefix(s, TokenPrefix) && len(s) > len(TokenPrefix)+20 }

// newAPIToken returns a token and the hash stored for it.
func newAPIToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return token, authn.HashToken(token), nil
}

// MachineUserInput creates a machine user.
type MachineUserInput struct {
	Name  string
	Email string // a contact, optional
}

// CreateMachineUser creates an account for an integration: no password, never
// an admin; grant it elements like any user, and give it API tokens.
func (s *Service) CreateMachineUser(ctx context.Context, a Actor, in MachineUserInput) (store.User, error) {
	if err := validateUsername(in.Name); err != nil {
		return store.User{}, err
	}
	var u store.User
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		u, err = store.InsertUser(ctx, tx, store.User{Username: strings.TrimSpace(in.Name), Email: strings.TrimSpace(in.Email),
			IsActive: true, Kind: store.UserService})
		if err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "user", idStr(u.ID), u,
			events.ControlChanged{Kind: events.KindUser, Op: events.OpCreate, ID: idStr(u.ID), UserID: i64(u.ID)})
	})
	return u, err
}

// TokenInput creates an API token.
type TokenInput struct {
	Name      string
	Scopes    []string
	ExpiresAt *time.Time
}

func machineUser(ctx context.Context, db store.DBTX, id int64) (store.User, error) {
	u, err := store.GetUser(ctx, db, id)
	if err != nil {
		return u, err
	}
	if u.Kind != store.UserService {
		return u, invalid("user", "API tokens belong to machine users")
	}
	return u, nil
}

// CreateAPIToken creates a token of a machine user and returns it with its
// secret, which isn't kept: this is the only time it can be read.
func (s *Service) CreateAPIToken(ctx context.Context, a Actor, userID int64, in TokenInput) (store.APIToken, string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 100 {
		return store.APIToken{}, "", invalid("name", "1-100 characters")
	}
	var scopes []string
	for _, sc := range in.Scopes {
		if !slices.Contains(store.Scopes, sc) {
			return store.APIToken{}, "", invalid("scopes", "each one of read, control, alerts")
		}
		if !slices.Contains(scopes, sc) {
			scopes = append(scopes, sc)
		}
	}
	if len(scopes) == 0 {
		return store.APIToken{}, "", invalid("scopes", "at least one")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		return store.APIToken{}, "", invalid("expires_at", "must be in the future")
	}
	token, hash, err := newAPIToken()
	if err != nil {
		return store.APIToken{}, "", err
	}
	var out store.APIToken
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := machineUser(ctx, tx, userID); err != nil {
			return err
		}
		have, err := store.ListAPITokens(ctx, tx, userID)
		if err != nil {
			return err
		}
		if len(have) >= MaxTokensPerUser {
			return invalid("name", "a machine user has at most 20 tokens; revoke one first")
		}
		if out, err = store.InsertAPIToken(ctx, tx, store.APIToken{UserID: userID, Name: name, Prefix: token[:len(TokenPrefix)+6],
			Scopes: scopes, ExpiresAt: in.ExpiresAt, CreatedBy: a.Name}, hash); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "api_token", out.ID.String(), out)
	})
	if err != nil {
		return store.APIToken{}, "", err
	}
	return out, token, nil
}

func (s *Service) ListAPITokens(ctx context.Context, userID int64) ([]store.APIToken, error) {
	if _, err := machineUser(ctx, s.Pool, userID); err != nil {
		return nil, err
	}
	return store.ListAPITokens(ctx, s.Pool, userID)
}

// RevokeAPIToken deletes a token: requests with it fail from now on.
func (s *Service) RevokeAPIToken(ctx context.Context, a Actor, userID int64, tokenID uuid.UUID) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.DeleteAPIToken(ctx, tx, userID, tokenID); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "api_token", tokenID.String(), map[string]any{"user_id": userID},
			events.ControlChanged{Kind: events.KindUser, Op: events.OpUpdate, ID: idStr(userID), UserID: i64(userID)})
	})
}

// TokenActor is how a token's requests appear in the audit log.
func TokenActor(p store.TokenPrincipal) Actor {
	id := p.ID
	return Actor{UserID: &id, Name: p.Username + " (token " + p.TokenName + ")"}
}
