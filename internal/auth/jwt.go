package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims of the access token.
type Claims struct {
	UserID uuid.UUID `json:"uid"`
	Email  string    `json:"email"`
	Role   string    `json:"role"`
	jwt.RegisteredClaims
}

// Signer issues and verifies access tokens.
type Signer struct {
	secret []byte
	ttl    time.Duration
}

// NewSigner creates a signer.
func NewSigner(secret string, ttl time.Duration) *Signer {
	return &Signer{secret: []byte(secret), ttl: ttl}
}

// Issue returns a signed access token.
func (s *Signer) Issue(userID uuid.UUID, email, role string) (string, time.Time, error) {
	exp := time.Now().Add(s.ttl)
	c := Claims{UserID: userID, Email: email, Role: role, RegisteredClaims: jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(exp), IssuedAt: jwt.NewNumericDate(time.Now()), Issuer: "supersbc",
	}}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
	return tok, exp, err
}

// Verify parses and validates an access token.
func (s *Signer) Verify(token string) (*Claims, error) {
	var c Claims
	t, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	}, jwt.WithIssuer("supersbc"), jwt.WithExpirationRequired())
	if err != nil || !t.Valid {
		return nil, errors.New("invalid token")
	}
	return &c, nil
}
