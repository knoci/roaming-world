package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	DefaultIssuer     = "roaming-world"
	DefaultAudience   = "roaming-world-api"
	DefaultAccessTTL  = 30 * time.Minute
	DefaultRefreshTTL = 7 * 24 * time.Hour

	minSigningKeyBytes = 32
	refreshTokenBytes  = 32
)

var (
	// ErrMissingToken indicates no Authorization header was supplied on the request.
	ErrMissingToken = errors.New("auth: missing authorization token")
	// ErrInvalidToken indicates the Authorization header or JWT is malformed, expired, or has an invalid signature/claims.
	ErrInvalidToken = errors.New("auth: invalid authorization token")
	// ErrTokenRevoked indicates the JWT has been explicitly blacklisted or superseded by a newer token version.
	ErrTokenRevoked = errors.New("auth: token has been revoked")
	// ErrInvalidSigningKey indicates JWT_SIGNING_KEY is missing, not valid standard base64, or shorter than 32 bytes.
	ErrInvalidSigningKey = errors.New("JWT_SIGNING_KEY must be standard base64 for at least 32 bytes")
	// ErrRefreshTokenInvalid indicates the refresh token does not exist, has expired, or was already consumed.
	ErrRefreshTokenInvalid = errors.New("auth: refresh token is invalid or already consumed")
)

var (
	keyCacheMu sync.RWMutex
	cachedKey  []byte
)

// Claims defines the standard JWT payload shared across all roaming-world services.
type Claims struct {
	UID          string `json:"uid"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Avatar       string `json:"avatar"`
	Role         string `json:"role"`
	TokenVersion int64  `json:"ver"`
	jwt.RegisteredClaims
}

// ResetKeyCacheForTest clears the in-memory JWT signing key cache.
// Intended exclusively for unit tests that modify JWT_SIGNING_KEY between cases.
func ResetKeyCacheForTest() {
	keyCacheMu.Lock()
	cachedKey = nil
	keyCacheMu.Unlock()
}

// ValidateJWTSigningKey decodes JWT_SIGNING_KEY (standard Base64, >= 32 bytes)
// and caches the decoded bytes in memory for subsequent token operations.
func ValidateJWTSigningKey() error {
	_, err := jwtSigningKey()
	return err
}

func jwtSigningKey() ([]byte, error) {
	keyCacheMu.RLock()
	if len(cachedKey) >= minSigningKeyBytes {
		key := cachedKey
		keyCacheMu.RUnlock()
		return key, nil
	}
	keyCacheMu.RUnlock()

	keyCacheMu.Lock()
	defer keyCacheMu.Unlock()
	if len(cachedKey) >= minSigningKeyBytes {
		return cachedKey, nil
	}

	raw := strings.TrimSpace(os.Getenv("JWT_SIGNING_KEY"))
	if raw == "" {
		return nil, ErrInvalidSigningKey
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) < minSigningKeyBytes {
		return nil, ErrInvalidSigningKey
	}

	cachedKey = make([]byte, len(key))
	copy(cachedKey, key)
	return cachedKey, nil
}

// GenerateAccessToken signs a standard HS256 JWT access token with unique jti,
// issuer, audience, and token version claims.
func GenerateAccessToken(uid, name, email, avatar, role string, tokenVersion int64, ttl time.Duration) (token string, jti string, err error) {
	if strings.TrimSpace(uid) == "" {
		return "", "", fmt.Errorf("%w: uid is required", ErrInvalidToken)
	}
	signingKey, err := jwtSigningKey()
	if err != nil {
		return "", "", err
	}

	if ttl <= 0 {
		ttl = DefaultAccessTTL
	}
	if tokenVersion <= 0 {
		tokenVersion = 1
	}

	now := time.Now()
	jti = uuid.NewString()
	claims := &Claims{
		UID:          uid,
		Name:         name,
		Email:        email,
		Avatar:       avatar,
		Role:         role,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Issuer:    DefaultIssuer,
			Audience:  jwt.ClaimStrings{DefaultAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}

	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(signingKey)
	if err != nil {
		return "", "", err
	}
	return signed, jti, nil
}

// ParseAccessToken verifies an HS256 JWT access token, enforcing HS256 algorithm allowlist,
// expiration, issuer, audience, and a non-empty UID claim.
func ParseAccessToken(tokenString string) (*Claims, error) {
	if strings.TrimSpace(tokenString) == "" {
		return nil, fmt.Errorf("%w: empty token", ErrInvalidToken)
	}
	signingKey, err := jwtSigningKey()
	if err != nil {
		return nil, err
	}

	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(_ *jwt.Token) (any, error) {
			return signingKey, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(DefaultIssuer),
		jwt.WithAudience(DefaultAudience),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	if strings.TrimSpace(claims.UID) == "" {
		return nil, fmt.Errorf("%w: missing uid claim", ErrInvalidToken)
	}
	return claims, nil
}

// GenerateRefreshToken generates 32 cryptographically random bytes encoded as base64.RawURLEncoding.
func GenerateRefreshToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("auth: generate refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashRefreshToken returns the hex-encoded SHA-256 digest of a refresh token.
// Only this digest should be persisted in Redis; never store raw refresh tokens.
func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
