package pkg

import (
	"context"

	"github.com/knoci/roaming-world/pkg/auth"
)

// Claims aliases the shared standard HS256 JWT claims defined in pkg/auth.
type Claims = auth.Claims

func ValidateJWTSigningKey() error {
	return auth.ValidateJWTSigningKey()
}

func ParseToken(tokenString string) (*Claims, error) {
	return auth.ParseAccessToken(tokenString)
}

func ParseClaimsFromContext(ctx context.Context) (*Claims, error) {
	return auth.ParseClaimsFromContext(ctx, nil)
}

func ExtractBearerToken(ctx context.Context) (string, bool) {
	return auth.ExtractBearerToken(ctx)
}
