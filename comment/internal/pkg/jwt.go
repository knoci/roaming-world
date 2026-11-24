package pkg

import (
	"context"

	"github.com/knoci/roaming-world/pkg/auth"
)

// Claims aliases the shared standard HS256 JWT claims from pkg/auth.
type Claims = auth.Claims

// ValidateJWTSigningKey delegates JWT signing key validation to pkg/auth.
func ValidateJWTSigningKey() error {
	return auth.ValidateJWTSigningKey()
}

// ParseToken verifies a standard HS256 JWT access token via pkg/auth.
func ParseToken(tokenString string) (*Claims, error) {
	return auth.ParseAccessToken(tokenString)
}

// ParseClaimsFromContext extracts and validates a Bearer JWT from ctx via pkg/auth.
func ParseClaimsFromContext(ctx context.Context) (*Claims, error) {
	return auth.ParseClaimsFromContext(ctx, nil)
}
