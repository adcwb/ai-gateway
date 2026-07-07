package pkg

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SessionClaims is the console session cookie payload (docs/design/04-multi-
// tenancy-and-auth.md): a self-contained, stateless JWT signed with HMAC —
// consistent with the project's "stateless gateway instances" principle, no
// server-side session store to invalidate/scale.
type SessionClaims struct {
	UserID          uint   `json:"uid"`
	Email           string `json:"email"`
	IsPlatformAdmin bool   `json:"padm"`
	jwt.RegisteredClaims
}

var ErrInvalidSession = errors.New("invalid or expired session")

// IssueSessionToken signs a session JWT valid for ttl.
func IssueSessionToken(secret []byte, userID uint, email string, isPlatformAdmin bool, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := SessionClaims{
		UserID:          userID,
		Email:           email,
		IsPlatformAdmin: isPlatformAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ParseSessionToken verifies signature + expiry and returns the claims.
func ParseSessionToken(secret []byte, raw string) (*SessionClaims, error) {
	claims := &SessionClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidSession
		}
		return secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidSession
	}
	return claims, nil
}
