package service

import (
	"context"
	"errors"
	"strings"

	"github.com/go-kratos/kratos/v2/metadata"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/knoci/roaming-world/food/internal/pkg"
)

func getJwtClaim(ctx context.Context) (*pkg.Claims, error) {
	var authHeader, ua string
	if req, ok := khttp.RequestFromServerContext(ctx); ok && req != nil {
		authHeader = req.Header.Get("Authorization")
		ua = req.Header.Get("User-Agent")
	} else if md, ok := metadata.FromServerContext(ctx); ok {
		authHeader = md.Get("authorization")
		ua = md.Get("user-agent")
	}
	if authHeader == "" {
		return nil, errors.New("missing token")
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return nil, errors.New("missing token")
	}
	return pkg.ParseToken(ua, parts[1])
}

func isAdmin(ctx context.Context) bool {
	claims, err := getJwtClaim(ctx)
	return err == nil && claims.Role == "admin"
}
