package pkg

import (
	"context"

	"github.com/knoci/roaming-world/pkg/auth"
)

// Claims 与 user 服务保持一致，委托给共享的 pkg/auth。
type Claims = auth.Claims

// ValidateJWTSigningKey 校验并缓存 JWT_SIGNING_KEY。
func ValidateJWTSigningKey() error {
	return auth.ValidateJWTSigningKey()
}

// ParseToken 校验标准 HS256 JWT（不再使用 UA-AES 封装）。
func ParseToken(tokenString string) (*Claims, error) {
	return auth.ParseAccessToken(tokenString)
}

// ParseClaimsFromContext 从 HTTP 或 gRPC 上下文提取并校验 JWT Claims。
func ParseClaimsFromContext(ctx context.Context, checker auth.RevocationChecker) (*Claims, error) {
	return auth.ParseClaimsFromContext(ctx, checker)
}
