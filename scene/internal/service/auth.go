package service

import (
	"context"

	"github.com/knoci/roaming-world/pkg/auth"
	"github.com/knoci/roaming-world/scene/internal/biz"
)

func getJwtClaim(ctx context.Context) (*auth.Claims, error) {
	return auth.ParseClaimsFromContext(ctx, nil)
}

func requireAdmin(ctx context.Context) error {
	claims, err := getJwtClaim(ctx)
	if err != nil {
		return biz.ErrUnauthorized
	}
	if claims.Role != "admin" {
		return biz.ErrForbidden
	}
	return nil
}
