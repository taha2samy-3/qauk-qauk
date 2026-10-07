package store

import (
	"context"
	"net/netip"
	"time"
)

const userCols = `id, username, email, password_hash, is_active, is_admin, created_at, last_login_at`

func GetUserByUsername(ctx context.Context, db DBTX, username string) (User, error) {
	return one[User](db.Query(ctx, `SELECT `+userCols+` FROM users WHERE username = $1`, username))
}

func GetUser(ctx context.Context, db DBTX, id int64) (User, error) {
	return one[User](db.Query(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func ListUsers(ctx context.Context, db DBTX) ([]User, error) {
	return many[User](db.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY id`))
}

func InsertUser(ctx context.Context, db DBTX, u User) (User, error) {
	return one[User](db.Query(ctx, `INSERT INTO users (username, email, password_hash, is_active, is_admin)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+userCols, u.Username, u.Email, u.PasswordHash, u.IsActive, u.IsAdmin))
}

func UpdateUser(ctx context.Context, db DBTX, u User) (User, error) {
	return one[User](db.Query(ctx, `UPDATE users SET username = $2, email = $3, password_hash = $4, is_active = $5, is_admin = $6
		WHERE id = $1 RETURNING `+userCols, u.ID, u.Username, u.Email, u.PasswordHash, u.IsActive, u.IsAdmin))
}

func SetPasswordHash(ctx context.Context, db DBTX, id int64, hash string) error {
	return execOne(db.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, hash))
}

func TouchLogin(ctx context.Context, db DBTX, id int64) error {
	return execOne(db.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id))
}

func DeleteUser(ctx context.Context, db DBTX, id int64) error {
	return execOne(db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id))
}

// --- Sessions ---

func InsertSession(ctx context.Context, db DBTX, hash []byte, userID int64, expires time.Time, ip, ua string) error {
	_, err := db.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at, ip, user_agent) VALUES ($1, $2, $3, $4, $5)`,
		hash, userID, expires, ip, ua)
	return mapErr(err)
}

// SessionUser returns the active user owning a valid session.
func SessionUser(ctx context.Context, db DBTX, hash []byte) (User, error) {
	return one[User](db.Query(ctx, `UPDATE sessions s SET last_seen_at = now()
		FROM users u
		WHERE s.token_hash = $1 AND s.expires_at > now() AND u.id = s.user_id AND u.is_active
		RETURNING u.id, u.username, u.email, u.password_hash, u.is_active, u.is_admin, u.created_at, u.last_login_at`, hash))
}

func DeleteSession(ctx context.Context, db DBTX, hash []byte) error {
	_, err := db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hash)
	return mapErr(err)
}

func DeleteUserSessions(ctx context.Context, db DBTX, userID int64) error {
	_, err := db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return mapErr(err)
}

func PurgeExpiredSessions(ctx context.Context, db DBTX) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	return tag.RowsAffected(), mapErr(err)
}

// ClientIP normalizes a remote address for storage.
func ClientIP(remoteAddr string) string {
	if ap, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return ap.Addr().String()
	}
	return remoteAddr
}

// --- Groups ---

func ListGroups(ctx context.Context, db DBTX) ([]Group, error) {
	return many[Group](db.Query(ctx, `SELECT id, name FROM groups ORDER BY id`))
}

func GetGroup(ctx context.Context, db DBTX, id int64) (Group, error) {
	return one[Group](db.Query(ctx, `SELECT id, name FROM groups WHERE id = $1`, id))
}

func GetGroupByName(ctx context.Context, db DBTX, name string) (Group, error) {
	return one[Group](db.Query(ctx, `SELECT id, name FROM groups WHERE name = $1`, name))
}

func InsertGroup(ctx context.Context, db DBTX, name string) (Group, error) {
	return one[Group](db.Query(ctx, `INSERT INTO groups (name) VALUES ($1) RETURNING id, name`, name))
}

func RenameGroup(ctx context.Context, db DBTX, id int64, name string) (Group, error) {
	return one[Group](db.Query(ctx, `UPDATE groups SET name = $2 WHERE id = $1 RETURNING id, name`, id, name))
}

func DeleteGroup(ctx context.Context, db DBTX, id int64) error {
	return execOne(db.Exec(ctx, `DELETE FROM groups WHERE id = $1`, id))
}

func AddGroupMember(ctx context.Context, db DBTX, groupID, userID int64) (bool, error) {
	tag, err := db.Exec(ctx, `INSERT INTO user_groups (user_id, group_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, groupID)
	return tag.RowsAffected() > 0, mapErr(err)
}

func RemoveGroupMember(ctx context.Context, db DBTX, groupID, userID int64) error {
	return execOne(db.Exec(ctx, `DELETE FROM user_groups WHERE user_id = $1 AND group_id = $2`, userID, groupID))
}

func ListGroupMembers(ctx context.Context, db DBTX, groupID int64) ([]User, error) {
	return many[User](db.Query(ctx, `SELECT u.id, u.username, u.email, u.password_hash, u.is_active, u.is_admin, u.created_at, u.last_login_at
		FROM users u JOIN user_groups ug ON ug.user_id = u.id WHERE ug.group_id = $1 ORDER BY u.id`, groupID))
}

func ListUserGroups(ctx context.Context, db DBTX, userID int64) ([]Group, error) {
	return many[Group](db.Query(ctx, `SELECT g.id, g.name FROM groups g JOIN user_groups ug ON ug.group_id = g.id
		WHERE ug.user_id = $1 ORDER BY g.id`, userID))
}
