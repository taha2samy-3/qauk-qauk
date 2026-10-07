// Package devtools seeds fixtures and runs hook actions for the black-box
// contract suite (server/contracttest, see its README for the schema).
// It is never used in production code paths.
package devtools

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

var actor = service.Actor{Name: "contract-seed"}

type fixtureDevice struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	KeyID         string `json:"key_id"`
	PrivateKeyPEM string `json:"private_key_pem"`
	Alg           string `json:"alg"`
}

type fixtureElement struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	DeviceID string          `json:"device_id"`
	Points   int             `json:"points"`
	Details  json.RawMessage `json:"details"`
}

type fixtureUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Cookie   string `json:"cookie"`
}

type fixture struct {
	WSBase   string                    `json:"ws_base"`
	Origin   string                    `json:"origin"`
	Paths    map[string]string         `json:"paths"`
	Devices  map[string]fixtureDevice  `json:"devices"`
	Elements map[string]fixtureElement `json:"elements"`
	Users    map[string]fixtureUser    `json:"users"`
	Groups   map[string]string         `json:"groups"`
	Grants   map[string]any            `json:"grants"`
}

// Seed wipes all data and creates the contract fixture.
func Seed(ctx context.Context, pool *pgxpool.Pool, out, wsBase, origin string, ttl time.Duration) error {
	if _, err := pool.Exec(ctx, `TRUNCATE users, groups, sessions, dashboards, jwt_public_keys, devices, elements, element_permissions,
		device_presence, device_connections, outbox, audit_log RESTART IDENTITY CASCADE`); err != nil {
		return fmt.Errorf("reset: %w", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE element_event`); err != nil {
		return fmt.Errorf("reset events: %w", err)
	}
	svc := service.New(pool)
	fx := fixture{
		WSBase: wsBase, Origin: origin,
		Paths:   map[string]string{"device": "/device/node_red/", "browser": "/browser/simple/"},
		Devices: map[string]fixtureDevice{}, Elements: map[string]fixtureElement{}, Users: map[string]fixtureUser{},
		Groups: map[string]string{"readers": "contract-readers"},
	}

	// --- devices and keys ---
	type devSpec struct {
		role, alg string
		key       string // "active", "inactive", "none"
	}
	specs := []devSpec{
		{"main", "RS256", "active"}, {"second", "ES256", "active"}, {"nokey", "ES256", "none"},
		{"inactive", "ES256", "inactive"}, {"disposable", "ES256", "active"}, {"rotate", "RS256", "active"},
		{"status", "ES256", "active"},
	}
	for _, s := range specs {
		privPEM, pubPEM, err := genKey(s.alg)
		if err != nil {
			return err
		}
		var keyID *uuid.UUID
		if s.key != "none" {
			k, err := svc.CreateKey(ctx, actor, "contract-"+s.role, pubPEM, nil)
			if err != nil {
				return fmt.Errorf("key %s: %w", s.role, err)
			}
			if s.key == "inactive" {
				off := false
				if _, err := svc.UpdateKey(ctx, actor, k.ID, service.KeyPatch{IsActive: &off}); err != nil {
					return err
				}
			}
			keyID = &k.ID
		}
		d, err := svc.CreateDevice(ctx, actor, service.DeviceInput{Name: "contract-" + s.role, PublicKeyID: keyID})
		if err != nil {
			return fmt.Errorf("device %s: %w", s.role, err)
		}
		fd := fixtureDevice{ID: d.ID.String(), Name: d.Name, PrivateKeyPEM: privPEM, Alg: s.alg}
		if keyID != nil {
			fd.KeyID = keyID.String()
		}
		fx.Devices[s.role] = fd
	}

	// --- elements ---
	type elSpec struct {
		role, device string
		points       int
		details      string
	}
	for _, s := range []elSpec{
		{"sensor", "main", 10, `{"title":"Contract Sensor","minValue":0,"maxValue":100}`},
		{"switch", "main", 1, ""}, {"history", "main", 5, ""}, {"foreign", "second", 10, ""},
		{"disposable", "disposable", 10, ""}, {"rotate", "rotate", 10, ""}, {"status", "status", 10, ""},
	} {
		devID := uuid.MustParse(fx.Devices[s.device].ID)
		var details json.RawMessage
		if s.details != "" {
			details = json.RawMessage(s.details)
		}
		e, err := svc.CreateElement(ctx, actor, service.ElementInput{DeviceID: devID, Name: "contract-" + s.role, Points: s.points, Details: details})
		if err != nil {
			return fmt.Errorf("element %s: %w", s.role, err)
		}
		fd := json.RawMessage("null")
		if details != nil {
			fd = details
		}
		fx.Elements[s.role] = fixtureElement{ID: e.ID.String(), Name: e.Name, DeviceID: devID.String(), Points: e.Points, Details: fd}
	}

	// --- users, group, grants, sessions ---
	readers, err := svc.CreateGroup(ctx, actor, fx.Groups["readers"])
	if err != nil {
		return err
	}
	grants := map[string]map[string]string{
		"rc":      {"sensor": "RC", "switch": "RC", "history": "RC", "status": "RC", "disposable": "RC", "rotate": "RC", "foreign": "R"},
		"r":       {"sensor": "R", "switch": "R"},
		"noperm":  {},
		"group_r": {},
		"revoke":  {"sensor": "RC"},
		"upgrade": {"switch": "R"},
		"b6":      {"sensor": "R", "switch": "R"},
		"b8":      {},
	}
	for _, role := range []string{"rc", "r", "noperm", "group_r", "revoke", "upgrade", "b6", "b8"} {
		u, err := svc.CreateUser(ctx, actor, service.UserInput{Username: "contract-" + role, Password: "contract-password-" + role, IsActive: true})
		if err != nil {
			return fmt.Errorf("user %s: %w", role, err)
		}
		for el, perm := range grants[role] {
			uid := u.ID
			if _, err := svc.SetPermission(ctx, actor, service.PermissionInput{ElementID: uuid.MustParse(fx.Elements[el].ID), UserID: &uid, Permission: perm}); err != nil {
				return err
			}
		}
		if role == "group_r" || role == "b8" {
			if err := svc.AddGroupMember(ctx, actor, readers.ID, u.ID); err != nil {
				return err
			}
		}
		token, hash, err := authn.NewSessionToken()
		if err != nil {
			return err
		}
		if err := store.InsertSession(ctx, pool, hash, u.ID, time.Now().Add(ttl), "127.0.0.1", "contract-seed"); err != nil {
			return err
		}
		fx.Users[role] = fixtureUser{ID: strconv.FormatInt(u.ID, 10), Username: u.Username, Cookie: authn.SessionCookie + "=" + token}
	}
	gid := readers.ID
	if _, err := svc.SetPermission(ctx, actor, service.PermissionInput{ElementID: uuid.MustParse(fx.Elements["sensor"].ID), GroupID: &gid, Permission: "R"}); err != nil {
		return err
	}
	fx.Grants = map[string]any{"users": grants, "groups": map[string]any{"readers": map[string]string{"sensor": "R"}}}

	b, err := json.MarshalIndent(fx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, b, 0o600)
}

func genKey(alg string) (privPEM, pubPEM string, err error) {
	var priv any
	switch alg {
	case "RS256":
		priv, err = rsa.GenerateKey(rand.Reader, 2048)
	case "ES256":
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	default:
		return "", "", fmt.Errorf("unsupported alg %s", alg)
	}
	if err != nil {
		return "", "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(priv.(crypto.Signer).Public())
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})), nil
}

// Hook runs one contract hook action through the service layer (same path as the API).
func Hook(ctx context.Context, pool *pgxpool.Pool, args []string) error {
	if len(args) == 0 {
		return errors.New("hook: missing action")
	}
	svc := service.New(pool)
	need := func(n int) error {
		if len(args)-1 != n {
			return fmt.Errorf("hook %s: want %d args, got %d", args[0], n, len(args)-1)
		}
		return nil
	}
	user := func(name string) (store.User, error) { return store.GetUserByUsername(ctx, pool, name) }
	switch args[0] {
	case "set-perm":
		if err := need(3); err != nil {
			return err
		}
		u, err := user(args[1])
		if err != nil {
			return err
		}
		el, err := uuid.Parse(args[2])
		if err != nil {
			return err
		}
		_, err = svc.SetPermission(ctx, actor, service.PermissionInput{ElementID: el, UserID: &u.ID, Permission: args[3]})
		return err
	case "revoke":
		if err := need(2); err != nil {
			return err
		}
		u, err := user(args[1])
		if err != nil {
			return err
		}
		el, err := uuid.Parse(args[2])
		if err != nil {
			return err
		}
		perms, err := store.ListPermissions(ctx, pool, store.PermissionFilter{ElementID: &el, UserID: &u.ID})
		if err != nil {
			return err
		}
		for _, p := range perms {
			if err := svc.DeletePermission(ctx, actor, p.ID); err != nil {
				return err
			}
		}
		return nil
	case "remove-from-group":
		if err := need(2); err != nil {
			return err
		}
		u, err := user(args[1])
		if err != nil {
			return err
		}
		g, err := store.GetGroupByName(ctx, pool, args[2])
		if err != nil {
			return err
		}
		return svc.RemoveGroupMember(ctx, actor, g.ID, u.ID)
	case "element-create":
		if err := need(2); err != nil {
			return err
		}
		dev, err := uuid.Parse(args[1])
		if err != nil {
			return err
		}
		points, err := strconv.Atoi(args[2])
		if err != nil {
			return err
		}
		e, err := svc.CreateElement(ctx, actor, service.ElementInput{DeviceID: dev, Name: "contract-new", Points: points})
		if err != nil {
			return err
		}
		fmt.Println(e.ID)
		return nil
	case "element-delete":
		if err := need(1); err != nil {
			return err
		}
		id, err := uuid.Parse(args[1])
		if err != nil {
			return err
		}
		return svc.DeleteElement(ctx, actor, id)
	case "device-delete":
		if err := need(1); err != nil {
			return err
		}
		id, err := uuid.Parse(args[1])
		if err != nil {
			return err
		}
		return svc.DeleteDevice(ctx, actor, id)
	case "key-touch":
		if err := need(1); err != nil {
			return err
		}
		id, err := uuid.Parse(args[1])
		if err != nil {
			return err
		}
		_, err = svc.UpdateKey(ctx, actor, id, service.KeyPatch{})
		return err
	}
	return fmt.Errorf("hook: unknown action %q", args[0])
}

func parsePrivate(pemText string) (any, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("invalid private key PEM")
	}
	return x509.ParsePKCS8PrivateKey(block.Bytes)
}
