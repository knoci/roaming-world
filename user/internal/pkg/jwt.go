package pkg

import (
	"time"
	
	nacos "github.com/knoci/roaming-world/user/internal/conf/nacos"
	jwt "github.com/golang-jwt/jwt/v5"
)

// Claims 包含自定义的 JWT 声明
type Claims struct {
	UID    string `json:"uid"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Avatar string `json:"avatar"`
	jwt.RegisteredClaims
}




// GenerateToken 生成 JWT token
func GenerateToken(uid, name, email, avatar string) (string, error) {
	c := nacos.GetConfig()
	secret := nacos.GetConfigString(c, "jwt.secret")
	if secret == "" {
		panic("jwt.secret configuration is required")
	}

	expiration := time.Duration(nacos.GetConfigInt(c, "jwt.expiration"))
	if expiration == 0 {
		expiration = time.Hour * 120 // 默认120小时
	}

	claims := &Claims{
		UID:    uid,
		Name:   name,
		Email:  email,
		Avatar: avatar,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ParseToken 解析 JWT token
func ParseToken(tokenString string) (*Claims, error) {
	c := nacos.GetConfig()
	// 从配置中读取 JWT 设置
	secret := nacos.GetConfigString(c, "jwt.secret")
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, jwt.ErrInvalidKey
}