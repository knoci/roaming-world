package pkg

import "github.com/knoci/roaming-world/pkg/auth"

// Claims 与共享 pkg/auth 保持一致，token 由 user 服务签发。
type Claims = auth.Claims

func ValidateJWTSigningKey() error {
	return auth.ValidateJWTSigningKey()
}

func ParseToken(tokenString string) (*Claims, error) {
	return auth.ParseAccessToken(tokenString)
}
