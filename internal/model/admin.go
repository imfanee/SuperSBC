package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// User is an admin UI or API user.
type User struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	Email        string     `json:"email" db:"email"`
	PasswordHash string     `json:"-" db:"password_hash"`
	Role         string     `json:"role" db:"role"`
	TOTPSecret   *string    `json:"-" db:"totp_secret"`
	TOTPEnabled  bool       `json:"totp_enabled" db:"totp_enabled"`
	Status       string     `json:"status" db:"status"`
	LastLoginAt  *time.Time `json:"last_login_at" db:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

// APIKey is a programmatic credential (hash stored, plaintext shown once).
type APIKey struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	UserID     uuid.UUID  `json:"user_id" db:"user_id"`
	Name       string     `json:"name" db:"name"`
	KeyPrefix  string     `json:"key_prefix" db:"key_prefix"`
	LastUsedAt *time.Time `json:"last_used_at" db:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at" db:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
}

// Session is a refresh token session.
type Session struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	UserID    uuid.UUID  `json:"user_id" db:"user_id"`
	ExpiresAt time.Time  `json:"expires_at" db:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at" db:"revoked_at"`
	RemoteIP  *string    `json:"remote_ip" db:"remote_ip"`
	UserAgent *string    `json:"user_agent" db:"user_agent"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}

// AuditEntry records who changed what.
type AuditEntry struct {
	ID         int64          `json:"id" db:"id"`
	ActorID    *uuid.UUID     `json:"actor_id" db:"actor_id"`
	ActorEmail *string        `json:"actor_email" db:"actor_email"`
	Action     string         `json:"action" db:"action"`
	EntityType string         `json:"entity_type" db:"entity_type"`
	EntityID   *string        `json:"entity_id" db:"entity_id"`
	Before     map[string]any `json:"before" db:"before"`
	After      map[string]any `json:"after" db:"after"`
	RemoteIP   *string        `json:"remote_ip" db:"remote_ip"`
	CreatedAt  time.Time      `json:"created_at" db:"created_at"`
}

// BlockedPrefix is a global (customer_id null) or per-customer block.
type BlockedPrefix struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	CustomerID *uuid.UUID `json:"customer_id" db:"customer_id"`
	Prefix     string     `json:"prefix" db:"prefix"`
	Reason     string     `json:"reason" db:"reason"`
	Enabled    bool       `json:"enabled" db:"enabled"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
}

// FXRate is one exchange rate: 1 base = rate quote.
type FXRate struct {
	ID            uuid.UUID       `json:"id" db:"id"`
	Base          string          `json:"base" db:"base"`
	Quote         string          `json:"quote" db:"quote"`
	Rate          decimal.Decimal `json:"rate" db:"rate"`
	EffectiveFrom time.Time       `json:"effective_from" db:"effective_from"`
	CreatedBy     *string         `json:"created_by" db:"created_by"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
}

// Setting is one system settings row.
type Setting struct {
	Key       string    `json:"key" db:"key"`
	Value     any       `json:"value" db:"value"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
	UpdatedBy *string   `json:"updated_by" db:"updated_by"`
}

// Notification is a recorded alert (delivery is a roadmap item).
type Notification struct {
	ID          int64          `json:"id" db:"id"`
	Kind        string         `json:"kind" db:"kind"`
	OwnerType   *string        `json:"owner_type" db:"owner_type"`
	OwnerID     *uuid.UUID     `json:"owner_id" db:"owner_id"`
	Payload     map[string]any `json:"payload" db:"payload"`
	DeliveredAt *time.Time     `json:"delivered_at" db:"delivered_at"`
	CreatedAt   time.Time      `json:"created_at" db:"created_at"`
}
