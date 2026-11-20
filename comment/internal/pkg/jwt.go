package pkg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"

	jwt "github.com/golang-jwt/jwt/v5"
)

func ValidateJWTSigningKey() error {
	_, err := jwtSigningKey()
	return err
}

func jwtSigningKey() ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("JWT_SIGNING_KEY"))
	if err != nil || len(key) < 32 {
		return nil, errors.New("JWT_SIGNING_KEY must be standard base64 for at least 32 bytes")
	}
	return key, nil
}

// Claims 包含自定义的 JWT 声明
type Claims struct {
	UID    string `json:"uid"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Avatar string `json:"avatar"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// ParseToken 解析加密的 JWT token，使用UA作为解密密钥
func ParseToken(ua, encryptedToken string) (*Claims, error) {
	signingKey, err := jwtSigningKey()
	if err != nil {
		return nil, err
	}

	// 使用UA作为密钥解密令牌
	tokenString, err := decryptToken(encryptedToken, ua)
	if err != nil {
		return nil, err
	}

	// 然后解析JWT
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(_ *jwt.Token) (interface{}, error) {
		return signingKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, jwt.ErrInvalidKey
}

// 解密函数，使用UA作为密钥
func decryptToken(encryptedToken, ua string) (string, error) {
	// 从UA生成固定长度的密钥
	key := deriveKeyFromUA(ua)

	ciphertext, err := base64.URLEncoding.DecodeString(encryptedToken)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// 从UA生成固定长度的密钥(32字节)
func deriveKeyFromUA(ua string) []byte {
	hash := sha256.New()
	hash.Write([]byte(ua))
	return hash.Sum(nil)
}
