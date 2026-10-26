package pkg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	nacos "github.com/knoci/roaming-world/article/internal/conf/nacos"
)

// Claims 与 user 服务保持一致，token 由 user 服务签发。
type Claims struct {
	UID    string `json:"uid"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Avatar string `json:"avatar"`
	jwt.RegisteredClaims
}

func GenerateToken(ua, uid, name, email, avatar string) (string, error) {
	c := nacos.GetConfig()
	jwtSecret := nacos.GetConfigString(c, "jwt.secret")
	if jwtSecret == "" {
		panic("jwt.secret configuration is required")
	}

	expiration := time.Duration(nacos.GetConfigInt(c, "jwt.expiration"))
	if expiration == 0 {
		expiration = time.Hour * 120
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

	tokenString, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(jwtSecret))
	if err != nil {
		return "", err
	}
	return encryptToken(tokenString, ua)
}

func ParseToken(ua, encryptedToken string) (*Claims, error) {
	c := nacos.GetConfig()
	jwtSecret := nacos.GetConfigString(c, "jwt.secret")

	tokenString, err := decryptToken(encryptedToken, ua)
	if err != nil {
		return nil, err
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(jwtSecret), nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, jwt.ErrInvalidKey
}

func encryptToken(tokenString, ua string) (string, error) {
	key := deriveKeyFromUA(ua)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(tokenString), nil)), nil
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
