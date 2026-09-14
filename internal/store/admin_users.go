package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/model"
)

const userCols = `id, email, password_hash, role, totp_secret, totp_enabled, status, last_login_at, created_at, updated_at`

// UserByEmail loads a user.
func (s *Store) UserByEmail(ctx context.Context, email string) (*model.User, error) {
	return one[model.User](ctx, s.pool, `SELECT `+userCols+` FROM users WHERE lower(email) = lower($1)`, email)
}

// UserByID loads a user.
func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	return one[model.User](ctx, s.pool, `SELECT `+userCols+` FROM users WHERE id = $1`, id)
}

// Users lists all users.
func (s *Store) Users(ctx context.Context) ([]model.User, error) {
	return many[model.User](ctx, s.pool, `SELECT `+userCols+` FROM users ORDER BY email`)
}

// CountUsers returns the number of users (bootstrap check).
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, wrapErr(err)
}

// CreateUser inserts a user.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role string) (*model.User, error) {
	return one[model.User](ctx, s.pool, `INSERT INTO users (email, password_hash, role) VALUES (lower($1), $2, $3) RETURNING `+userCols, email, passwordHash, role)
}

// UpdateUser updates role and status.
func (s *Store) UpdateUser(ctx context.Context, id uuid.UUID, role, status string) (*model.User, error) {
	return one[model.User](ctx, s.pool, `UPDATE users SET role = $2, status = $3 WHERE id = $1 RETURNING `+userCols, id, role, status)
}

// SetPassword updates the password hash and revokes all sessions.
func (s *Store) SetPassword(ctx context.Context, id uuid.UUID, hash string) error {
	return s.WithTx(ctx, func(tx Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, hash); err != nil {
			return wrapErr(err)
		}
		_, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, id)
		return wrapErr(err)
	})
}

// SetTOTP stores the secret (enabled=false until confirmed) or clears it.
func (s *Store) SetTOTP(ctx context.Context, id uuid.UUID, secret *string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET totp_secret = $2, totp_enabled = $3 WHERE id = $1`, id, secret, enabled)
	return wrapErr(err)
}

// DeleteUser removes a user.
func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchLogin records a successful login time.
func (s *Store) TouchLogin(ctx context.Context, id uuid.UUID) {
	_, _ = s.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
}

// ---- sessions (refresh tokens) ----

// CreateSession stores a hashed refresh token.
func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, tokenHash string, expires time.Time, ip, ua string) (*model.Session, error) {
	return one[model.Session](ctx, s.pool, `INSERT INTO sessions (user_id, token_hash, expires_at, remote_ip, user_agent) VALUES ($1, $2, $3, NULLIF($4, '')::inet, $5)
		RETURNING id, user_id, expires_at, revoked_at, remote_ip::text AS remote_ip, user_agent, created_at`, userID, tokenHash, expires, ip, ua)
}

// SessionByToken loads a live session by refresh token hash.
func (s *Store) SessionByToken(ctx context.Context, tokenHash string) (*model.Session, error) {
	return one[model.Session](ctx, s.pool, `SELECT id, user_id, expires_at, revoked_at, remote_ip::text AS remote_ip, user_agent, created_at
		FROM sessions WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`, tokenHash)
}

// RevokeSession marks a session revoked.
func (s *Store) RevokeSession(ctx context.Context, tokenHash string) {
	_, _ = s.pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
}

// RotateSession revokes the old session and creates a new one atomically.
func (s *Store) RotateSession(ctx context.Context, oldHash, newHash string, expires time.Time, ip, ua string) (*model.Session, error) {
	var out *model.Session
	err := s.WithTx(ctx, func(tx Tx) error {
		var userID uuid.UUID
		if err := tx.QueryRow(ctx, `UPDATE sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now() RETURNING user_id`, oldHash).Scan(&userID); err != nil {
			return wrapErr(err)
		}
		row := tx.QueryRow(ctx, `INSERT INTO sessions (user_id, token_hash, expires_at, remote_ip, user_agent) VALUES ($1, $2, $3, NULLIF($4, '')::inet, $5)
			RETURNING id, user_id, expires_at, revoked_at, remote_ip::text, user_agent, created_at`, userID, newHash, expires, ip, ua)
		var sess model.Session
		if err := row.Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt, &sess.RevokedAt, &sess.RemoteIP, &sess.UserAgent, &sess.CreatedAt); err != nil {
			return wrapErr(err)
		}
		out = &sess
		return nil
	})
	return out, err
}

// ---- login rate limiting ----

// RecordLoginAttempt stores an attempt.
func (s *Store) RecordLoginAttempt(ctx context.Context, email, ip string, success bool) {
	_, _ = s.pool.Exec(ctx, `INSERT INTO login_attempts (email, remote_ip, success) VALUES (lower($1), NULLIF($2, '')::inet, $3)`, email, ip, success)
}

// RecentFailedLogins counts failures in the window per email and per ip.
func (s *Store) RecentFailedLogins(ctx context.Context, email, ip string, window time.Duration) (byEmail, byIP int, err error) {
	err = s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM login_attempts WHERE NOT success AND created_at > now() - $3::interval AND lower(email) = lower($1)),
		(SELECT count(*) FROM login_attempts WHERE NOT success AND created_at > now() - $3::interval AND remote_ip = NULLIF($2, '')::inet)`, email, ip, window).Scan(&byEmail, &byIP)
	return byEmail, byIP, wrapErr(err)
}

// ---- api keys ----

// CreateAPIKey stores a key hash.
func (s *Store) CreateAPIKey(ctx context.Context, userID uuid.UUID, name, prefix, hash string) (*model.APIKey, error) {
	return one[model.APIKey](ctx, s.pool, `INSERT INTO api_keys (user_id, name, key_prefix, key_hash) VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, name, key_prefix, last_used_at, revoked_at, created_at`, userID, name, prefix, hash)
}

// APIKeys lists keys (all users for admins, own for others).
func (s *Store) APIKeys(ctx context.Context, userID *uuid.UUID) ([]model.APIKey, error) {
	if userID == nil {
		return many[model.APIKey](ctx, s.pool, `SELECT id, user_id, name, key_prefix, last_used_at, revoked_at, created_at FROM api_keys ORDER BY created_at DESC`)
	}
	return many[model.APIKey](ctx, s.pool, `SELECT id, user_id, name, key_prefix, last_used_at, revoked_at, created_at FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC`, *userID)
}

// UserByAPIKeyHash resolves a live key to its user.
func (s *Store) UserByAPIKeyHash(ctx context.Context, hash string) (*model.User, error) {
	u, err := one[model.User](ctx, s.pool, `SELECT u.id, u.email, u.password_hash, u.role, u.totp_secret, u.totp_enabled, u.status, u.last_login_at, u.created_at, u.updated_at
		FROM api_keys k JOIN users u ON u.id = k.user_id WHERE k.key_hash = $1 AND k.revoked_at IS NULL AND u.status = 'active'`, hash)
	if err != nil {
		return nil, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE key_hash = $1`, hash)
	return u, nil
}

// RevokeAPIKey revokes a key.
func (s *Store) RevokeAPIKey(ctx context.Context, id uuid.UUID, userID *uuid.UUID) error {
	var tag interface{ RowsAffected() int64 }
	var err error
	if userID == nil {
		tag, err = s.pool.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	} else {
		tag, err = s.pool.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, id, *userID)
	}
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- audit log ----

// Audit appends an entry.
func (s *Store) Audit(ctx context.Context, actorID *uuid.UUID, actorEmail, action, entityType, entityID string, before, after any, ip string) {
	var b, a []byte
	if before != nil {
		b, _ = json.Marshal(before)
	}
	if after != nil {
		a, _ = json.Marshal(after)
	}
	var bs, as *string
	if b != nil {
		x := string(b)
		bs = &x
	}
	if a != nil {
		x := string(a)
		as = &x
	}
	_, _ = s.pool.Exec(ctx, `INSERT INTO audit_log (actor_id, actor_email, action, entity_type, entity_id, before, after, remote_ip)
		VALUES ($1, NULLIF($2, ''), $3, $4, NULLIF($5, ''), $6::jsonb, $7::jsonb, NULLIF($8, '')::inet)`, actorID, actorEmail, action, entityType, entityID, bs, as, ip)
}

// AuditLog pages through entries.
func (s *Store) AuditLog(ctx context.Context, entityType, entityID, actor string, p Page) (*Listing[model.AuditEntry], error) {
	p = p.Normalize()
	var conds []string
	var args []any
	if entityType != "" {
		args = append(args, entityType)
		conds = append(conds, fmt.Sprintf("entity_type = $%d", len(args)))
	}
	if entityID != "" {
		args = append(args, entityID)
		conds = append(conds, fmt.Sprintf("entity_id = $%d", len(args)))
	}
	if actor != "" {
		args = append(args, "%"+actor+"%")
		conds = append(conds, fmt.Sprintf("actor_email ILIKE $%d", len(args)))
	}
	var total int64
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_log"+where(conds), args...).Scan(&total); err != nil {
		return nil, wrapErr(err)
	}
	args = append(args, p.PerPage, p.Offset())
	items, err := many[model.AuditEntry](ctx, s.pool, `SELECT id, actor_id, actor_email, action, entity_type, entity_id, COALESCE(before, 'null'::jsonb) AS before, COALESCE(after, 'null'::jsonb) AS after, remote_ip::text AS remote_ip, created_at
		FROM audit_log`+where(conds)+fmt.Sprintf(" ORDER BY id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	return &Listing[model.AuditEntry]{Items: items, Total: total, Page: p.Page, PerPage: p.PerPage}, nil
}

// ---- settings ----

// Settings returns all settings rows.
func (s *Store) Settings(ctx context.Context) ([]model.Setting, error) {
	return many[model.Setting](ctx, s.pool, `SELECT key, value, updated_at, updated_by FROM system_settings ORDER BY key`)
}

// SetSetting upserts a setting.
func (s *Store) SetSetting(ctx context.Context, key string, value any, by string) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO system_settings (key, value, updated_by) VALUES ($1, $2::jsonb, $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by`, key, string(b), by)
	return wrapErr(err)
}

// Notifications lists recent notifications.
func (s *Store) Notifications(ctx context.Context, limit int) ([]model.Notification, error) {
	return many[model.Notification](ctx, s.pool, `SELECT id, kind, owner_type::text AS owner_type, owner_id, payload, delivered_at, created_at FROM notifications ORDER BY id DESC LIMIT $1`, limit)
}
