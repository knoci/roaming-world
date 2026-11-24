package pkg

import (
	"time"

	"github.com/knoci/roaming-world/pkg/auth"
)

const (
	DefaultAccessTTL  = auth.DefaultAccessTTL
	DefaultRefreshTTL = auth.DefaultRefreshTTL
)

// Claims aliases the shared standard JWT claims from pkg/auth.
type Claims = auth.Claims

// ValidateJWTSigningKey validates JWT_SIGNING_KEY at startup.
func ValidateJWTSigningKey() error {
	return auth.ValidateJWTSigningKey()
}

// GenerateAccessToken signs a standard short-lived HS256 JWT access token with jti and token version.
func GenerateAccessToken(uid, name, email, avatar, role string, tokenVersion int64, ttl time.Duration) (token string, jti string, err error) {
	return auth.GenerateAccessToken(uid, name, email, avatar, role, tokenVersion, ttl)
}

// GenerateToken signs a standard short-lived HS256 JWT access token using DefaultAccessTTL.
func GenerateToken(uid, name, email, avatar, role string, tokenVersion int64) (token string, jti string, err error) {
	return auth.GenerateAccessToken(uid, name, email, avatar, role, tokenVersion, auth.DefaultAccessTTL)
}

// ParseAccessToken verifies a standard HS256 JWT access token.
func ParseAccessToken(tokenString string) (*Claims, error) {
	return auth.ParseAccessToken(tokenString)
}

// ParseToken verifies a standard HS256 JWT access token.
func ParseToken(tokenString string) (*Claims, error) {
	return auth.ParseAccessToken(tokenString)
}

// GenerateRefreshToken generates a cryptographically random opaque refresh token.
func GenerateRefreshToken() (string, error) {
	return auth.GenerateRefreshToken()
}

// HashRefreshToken computes the SHA-256 hex digest of a refresh token.
func HashRefreshToken(token string) string {
	return auth.HashRefreshToken(token)
}

// ResetKeyCacheForTest clears the cached signing key between unit tests.
func ResetKeyCacheForTest() {
	auth.ResetKeyCacheForTest()
}
