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

// Claims 与 user 服务保持一致，token 由 user 服务签发。
type Claims struct {
	UID    string `json:"uid"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Avatar string `json:"avatar"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func ParseToken(ua, encryptedToken string) (*Claims, error) {
	signingKey, err := jwtSigningKey()
	if err != nil {
		return nil, err
	}

	tokenString, err := decryptToken(encryptedToken, ua)
	if err != nil {
		return nil, err
	}

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

func decryptToken(encryptedToken, ua string) (string, error) {
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

func deriveKeyFromUA(ua string) []byte {
	h := sha256.New()
	h.Write([]byte(ua))
	return h.Sum(nil)
}
