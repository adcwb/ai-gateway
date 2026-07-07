package pkg

import (
	"testing"
	"time"
)

func TestSessionTokenRoundTrip(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-ok")
	token, err := IssueSessionToken(secret, 42, "alice@example.com", true, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := ParseSessionToken(secret, token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.UserID != 42 || claims.Email != "alice@example.com" || !claims.IsPlatformAdmin {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestSessionTokenExpired(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-ok")
	token, err := IssueSessionToken(secret, 1, "a@b.com", false, -time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := ParseSessionToken(secret, token); err == nil {
		t.Fatal("expected expired token to fail verification")
	}
}

func TestSessionTokenWrongSecret(t *testing.T) {
	token, err := IssueSessionToken([]byte("secret-a-32-bytes-padded-here!!"), 1, "a@b.com", false, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := ParseSessionToken([]byte("secret-b-different-32-bytes!!!!"), token); err == nil {
		t.Fatal("expected signature mismatch to fail verification")
	}
}

func TestSessionTokenTampered(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-ok")
	token, err := IssueSessionToken(secret, 1, "a@b.com", false, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Flip a character in the middle of the token (payload/signature boundary)
	// rather than the very last character: base64url's final character can
	// encode unused padding bits, so mutating it doesn't always change the
	// decoded bytes — a flaky way to "corrupt" a token.
	mid := len(token) / 2
	flipped := byte('x')
	if token[mid] == 'x' {
		flipped = 'y'
	}
	tampered := token[:mid] + string(flipped) + token[mid+1:]
	if _, err := ParseSessionToken(secret, tampered); err == nil {
		t.Fatal("expected tampered token to fail verification")
	}
}
