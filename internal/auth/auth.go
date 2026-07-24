package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Claims is the JWT payload carried by every authenticated request.
// It embeds the user's roles and attribute map so the permission engine can
// enforce governance without a per-query database lookup.
type Claims struct {
	UserID      string              `json:"uid"`
	Username    string              `json:"usr"`
	DisplayName string              `json:"name"`
	Roles       []string            `json:"roles"`
	Attributes  map[string]string   `json:"attrs"`
	jwt.RegisteredClaims
}

// HashPassword returns a bcrypt hash of the plaintext password.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword verifies a plaintext password against a bcrypt hash.
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// GenerateToken signs a JWT for the given claims.
func GenerateToken(c *Claims, secret, expiry string) (string, error) {
	d, err := time.ParseDuration(expiry)
	if err != nil {
		d = 24 * time.Hour
	}
	c.RegisteredClaims = jwt.RegisteredClaims{
		Subject:   c.UserID,
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(d)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return tok.SignedString([]byte(secret))
}

// ParseToken validates and decodes a JWT string.
func ParseToken(signed, secret string) (*Claims, error) {
	c := &Claims{}
	tok, err := jwt.ParseWithClaims(signed, c, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return c, nil
}

// IsAdmin reports whether the principal holds the built-in admin role.
// The admin role is a superuser that bypasses per-table governance (used by
// the control plane to manage and to run unrestricted diagnostics).
func (c *Claims) IsAdmin() bool {
	for _, r := range c.Roles {
		if strings.EqualFold(r, "admin") {
			return true
		}
	}
	return false
}

// Attribute returns a user attribute from the embedded claim map.
func (c *Claims) Attribute(key string) string {
	return c.Attributes[key]
}
