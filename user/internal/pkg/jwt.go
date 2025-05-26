package pkg

import (
	"time"

	"github.com/go-kratos/kratos/v2/config"
	nacos "github.com/knoci/roaming-world/user/internal/conf/nacos"
	jwt "github.com/golang-jwt/jwt/v5"
)

// Claims 包含自定义的 JWT 声明
type Claims struct {
	UID    string `json:"uid"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
	jwt.RegisteredClaims
}

// JWT 服务结构体
type JWT struct {
	secret     []byte
	expiration time.Duration
}

// NewJWT 创建新的 JWT 实例
func NewJWT(c config.Config) *JWT {
	// 从配置中读取 JWT 设置
	secret := nacos.GetConfigString(c, "jwt.secret")
	if secret == "" {
		panic("jwt.secret configuration is required")
	}

	expiration := time.Duration(nacos.GetConfigInt(c, "jwt.expiration"))
	if expiration == 0 {
		expiration = time.Hour * 120 // 默认120小时
	}

	return &JWT{
		secret:     []byte(secret),
		expiration: expiration,
	}
}

// GenerateToken 生成 JWT token
func (j *JWT) GenerateToken(uid, name, avatar string) (string, error) {
	claims := &Claims{
		UID:    uid,
		Name:   name,
		Avatar: avatar,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(j.expiration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

// ParseToken 解析 JWT token
func (j *JWT) ParseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return j.secret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, jwt.ErrInvalidKey
}