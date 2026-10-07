// Package importdjango copies data from the legacy Django database into the Go
// schema. It preserves every UUID, user id and password hash (devices keep
// working, users keep their passwords) and is idempotent: re-running it
// upserts. It does not emit control events; run it before cutover.
package importdjango

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taha2samy/quackquack/server/internal/db"
	"github.com/taha2samy/quackquack/server/internal/keys"
)

type Report struct {
	Users, Groups, Memberships, Keys, Devices, Elements, Styles, Permissions int
	Warnings                                                                 []string
}

func Run(ctx context.Context, djangoURL string, dst *pgxpool.Pool, log *slog.Logger) (*Report, error) {
	src, err := pgx.Connect(ctx, djangoURL)
	if err != nil {
		return nil, fmt.Errorf("connect django db: %w", err)
	}
	defer func() { _ = src.Close(ctx) }()

	rep := &Report{}
	err = db.InTx(ctx, dst, func(tx pgx.Tx) error {
		steps := []struct {
			name string
			fn   func(context.Context, *pgx.Conn, pgx.Tx, *Report) error
		}{
			{"users", importUsers}, {"groups", importGroups}, {"memberships", importMemberships},
			{"keys", importKeys}, {"devices", importDevices}, {"elements", importElements},
			{"styles", importStyles}, {"permissions", importPermissions},
		}
		for _, s := range steps {
			if err := s.fn(ctx, src, tx, rep); err != nil {
				return fmt.Errorf("import %s: %w", s.name, err)
			}
			log.Info("import step done", "step", s.name)
		}
		for _, seq := range [][2]string{{"users", "id"}, {"groups", "id"}} {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s', '%s'), GREATEST((SELECT max(%s) FROM %s), 1))`,
				seq[0], seq[1], seq[1], seq[0])); err != nil {
				return err
			}
		}
		return nil
	})
	return rep, err
}

func importUsers(ctx context.Context, src *pgx.Conn, tx pgx.Tx, rep *Report) error {
	rows, err := src.Query(ctx, `SELECT id, username, email, password, is_active, is_superuser, date_joined, last_login FROM auth_user`)
	if err != nil {
		return err
	}
	type u struct {
		ID                int64
		Username, Email   string
		Password          string
		Active, Superuser bool
		Joined            time.Time
		LastLogin         *time.Time
	}
	us, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (u, error) {
		var x u
		err := r.Scan(&x.ID, &x.Username, &x.Email, &x.Password, &x.Active, &x.Superuser, &x.Joined, &x.LastLogin)
		return x, err
	})
	if err != nil {
		return err
	}
	for _, x := range us {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, username, email, password_hash, is_active, is_admin, created_at, last_login_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO UPDATE SET username = EXCLUDED.username, email = EXCLUDED.email, password_hash = EXCLUDED.password_hash,
				is_active = EXCLUDED.is_active, is_admin = EXCLUDED.is_admin, last_login_at = EXCLUDED.last_login_at`,
			x.ID, x.Username, x.Email, x.Password, x.Active, x.Superuser, x.Joined, x.LastLogin); err != nil {
			return err
		}
	}
	rep.Users = len(us)
	return nil
}

func importGroups(ctx context.Context, src *pgx.Conn, tx pgx.Tx, rep *Report) error {
	rows, err := src.Query(ctx, `SELECT id, name FROM auth_group`)
	if err != nil {
		return err
	}
	n := 0
	var id int64
	var name string
	if _, err := pgx.ForEachRow(rows, []any{&id, &name}, func() error {
		n++
		_, err := tx.Exec(ctx, `INSERT INTO groups (id, name) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name`, id, name)
		return err
	}); err != nil {
		return err
	}
	rep.Groups = n
	return nil
}

func importMemberships(ctx context.Context, src *pgx.Conn, tx pgx.Tx, rep *Report) error {
	rows, err := src.Query(ctx, `SELECT user_id, group_id FROM auth_user_groups`)
	if err != nil {
		return err
	}
	var uid, gid int64
	_, err = pgx.ForEachRow(rows, []any{&uid, &gid}, func() error {
		rep.Memberships++
		_, err := tx.Exec(ctx, `INSERT INTO user_groups (user_id, group_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, uid, gid)
		return err
	})
	return err
}

func importKeys(ctx context.Context, src *pgx.Conn, tx pgx.Tx, rep *Report) error {
	rows, err := src.Query(ctx, `SELECT id, name, public_key, is_active, created_at FROM node_red_jwtpublickey`)
	if err != nil {
		return err
	}
	var id uuid.UUID
	var name, pemText string
	var active bool
	var created time.Time
	_, err = pgx.ForEachRow(rows, []any{&id, &name, &pemText, &active, &created}, func() error {
		info, aerr := keys.Analyze(pemText)
		if aerr != nil {
			// Keep the device working: fall back to whatever the key parses as.
			pub, perr := keys.ParsePublic(pemText)
			if perr != nil {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("key %s (%s) skipped: %v", id, name, aerr))
				return nil
			}
			info = keys.Info{Algorithm: "RS256"}
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("key %s (%s) imported but fails current policy: %v (%T)", id, name, aerr, pub))
		}
		rep.Keys++
		_, err := tx.Exec(ctx, `INSERT INTO jwt_public_keys (id, name, pem, algorithm, key_size, is_active, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, pem = EXCLUDED.pem, algorithm = EXCLUDED.algorithm,
				key_size = EXCLUDED.key_size, is_active = EXCLUDED.is_active`,
			id, name, pemText, info.Algorithm, info.KeySize, active, created)
		return err
	})
	return err
}

func importDevices(ctx context.Context, src *pgx.Conn, tx pgx.Tx, rep *Report) error {
	rows, err := src.Query(ctx, `SELECT id, name, COALESCE(description, ''), public_key_id FROM node_red_device`)
	if err != nil {
		return err
	}
	var id uuid.UUID
	var name, desc string
	var key *uuid.UUID
	_, err = pgx.ForEachRow(rows, []any{&id, &name, &desc, &key}, func() error {
		rep.Devices++
		_, err := tx.Exec(ctx, `INSERT INTO devices (id, name, description, public_key_id) VALUES ($1, $2, $3,
				(SELECT id FROM jwt_public_keys WHERE id = $4))
			ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description, public_key_id = EXCLUDED.public_key_id`,
			id, name, desc, key)
		return err
	})
	return err
}

func importElements(ctx context.Context, src *pgx.Conn, tx pgx.Tx, rep *Report) error {
	rows, err := src.Query(ctx, `SELECT id, device_id, name, points, COALESCE(description, ''), details, created_at FROM node_red_element`)
	if err != nil {
		return err
	}
	var id, dev uuid.UUID
	var name, desc string
	var points int
	var details json.RawMessage
	var created time.Time
	_, err = pgx.ForEachRow(rows, []any{&id, &dev, &name, &points, &desc, &details, &created}, func() error {
		rep.Elements++
		_, err := tx.Exec(ctx, `INSERT INTO elements (id, device_id, name, points, description, details, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET device_id = EXCLUDED.device_id, name = EXCLUDED.name, points = EXCLUDED.points,
				description = EXCLUDED.description, details = EXCLUDED.details`,
			id, dev, name, points, desc, nullable(details), created)
		return err
	})
	return err
}

func importStyles(ctx context.Context, src *pgx.Conn, tx pgx.Tx, rep *Report) error {
	// Styles have no natural key: replace them per element.
	rows, err := src.Query(ctx, `SELECT element_id, name, details FROM node_red_elementdetailsstyle ORDER BY id`)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM element_styles WHERE element_id IN (SELECT id FROM elements)`); err != nil {
		return err
	}
	var el uuid.UUID
	var name string
	var details json.RawMessage
	_, err = pgx.ForEachRow(rows, []any{&el, &name, &details}, func() error {
		rep.Styles++
		_, err := tx.Exec(ctx, `INSERT INTO element_styles (element_id, name, details) VALUES ($1, $2, $3)`, el, name, nullable(details))
		return err
	})
	return err
}

func importPermissions(ctx context.Context, src *pgx.Conn, tx pgx.Tx, rep *Report) error {
	for _, q := range []struct{ sql, col, conflict string }{
		{`SELECT element_id, user_id, permissions FROM node_red_elementpermissionsuser`, "user_id", "(element_id, user_id) WHERE user_id IS NOT NULL"},
		{`SELECT element_id, group_id, permissions FROM node_red_elementpermissionsgroup`, "group_id", "(element_id, group_id) WHERE group_id IS NOT NULL"},
	} {
		rows, err := src.Query(ctx, q.sql)
		if err != nil {
			return err
		}
		var el uuid.UUID
		var subject int64
		var perm string
		if _, err := pgx.ForEachRow(rows, []any{&el, &subject, &perm}, func() error {
			rep.Permissions++
			_, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO element_permissions (element_id, %s, permission) VALUES ($1, $2, $3)
				ON CONFLICT %s DO UPDATE SET permission = EXCLUDED.permission`, q.col, q.conflict), el, subject, perm)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func nullable(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}
