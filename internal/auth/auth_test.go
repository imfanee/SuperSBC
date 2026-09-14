package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery") {
		t.Fatal("verify failed")
	}
	if VerifyPassword(h, "wrong") || VerifyPassword("garbage", "x") {
		t.Fatal("verify accepted wrong password")
	}
	if CheckPasswordPolicy("short") == nil {
		t.Fatal("policy")
	}
}

func TestJWT(t *testing.T) {
	s := NewSigner("secret-secret-secret-secret-1234", time.Minute)
	id := uuid.New()
	tok, _, err := s.Issue(id, "a@b.c", "admin")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok)
	if err != nil || c.UserID != id || c.Role != "admin" {
		t.Fatalf("verify: %v %+v", err, c)
	}
	if _, err := NewSigner("other-secret-other-secret-123456", time.Minute).Verify(tok); err == nil {
		t.Fatal("wrong secret accepted")
	}
	exp := NewSigner("secret-secret-secret-secret-1234", -time.Minute)
	tok2, _, _ := exp.Issue(id, "a@b.c", "admin")
	if _, err := s.Verify(tok2); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestAPIKey(t *testing.T) {
	key, prefix, hash, err := NewAPIKey()
	if err != nil || len(prefix) != 12 || HashToken(key) != hash || key[:4] != "sbc_" {
		t.Fatalf("api key: %v %s %s", err, key, prefix)
	}
}
