package pkg

import (
	"github.com/knoci/roaming-world/pkg/auth"
)

// Claims aliases the standard JWT claims shared across all roaming-world services.
type Claims = auth.Claims

// ValidateJWTSigningKey validates JWT_SIGNING_KEY via pkg/auth.
func ValidateJWTSigningKey() error {
	return auth.ValidateJWTSigningKey()
}

// ParseToken verifies a standard HS256 JWT access token via pkg/auth.
func ParseToken(tokenString string) (*Claims, error) {
	return auth.ParseAccessToken(tokenString)
}
