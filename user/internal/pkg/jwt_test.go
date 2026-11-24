package pkg

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/knoci/roaming-world/pkg/auth"
)

func TestStandardJWTIssueParseAndClaims(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(key))
	ResetKeyCacheForTest()
	t.Cleanup(ResetKeyCacheForTest)

	token, jti, err := GenerateAccessToken("uid-123", "alice", "alice@example.com", "https://cdn.example.com/a.png", "user", 4, 15*time.Minute)
	if err != nil {
		t.Fatalf("GenerateAccessToken failed: %v", err)
	}
	if jti == "" {
		t.Fatal("expected non-empty jti")
	}
	// Standard HS256 JWT has 3 dot-separated segments (not UA-AES ciphertext).
	if parts := strings.Split(token, "."); len(parts) != 3 {
		t.Fatalf("expected standard 3-segment JWT, got %d segments", len(parts))
	}

	claims, err := ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken failed: %v", err)
	}
	if claims.UID != "uid-123" || claims.Name != "alice" || claims.Email != "alice@example.com" || claims.Role != "user" {
		t.Fatalf("unexpected claims payload: %+v", claims)
	}
	if claims.TokenVersion != 4 {
		t.Fatalf("expected TokenVersion=4, got %d", claims.TokenVersion)
	}
	if claims.ID != jti {
		t.Fatalf("expected jti=%q, got %q", jti, claims.ID)
	}
	if claims.Issuer != auth.DefaultIssuer {
		t.Fatalf("expected issuer=%q, got %q", auth.DefaultIssuer, claims.Issuer)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != auth.DefaultAudience {
		t.Fatalf("expected audience=[%q], got %v", auth.DefaultAudience, claims.Audience)
	}
	if claims.ExpiresAt == nil || time.Until(claims.ExpiresAt.Time) <= 0 || time.Until(claims.ExpiresAt.Time) > 16*time.Minute {
		t.Fatalf("unexpected expiration: %v", claims.ExpiresAt)
	}
}

func TestJWTSigningKeyRotationAndValidation(t *testing.T) {
	oldKey := bytes.Repeat([]byte{1}, 32)
	newKey := bytes.Repeat([]byte{2}, 32)
	t.Cleanup(ResetKeyCacheForTest)

	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(oldKey))
	ResetKeyCacheForTest()

	oldToken, _, err := GenerateToken("fixture-user", "bob", "bob@example.com", "", "user", 2)
	if err != nil {
		t.Fatalf("GenerateToken with old key failed: %v", err)
	}
	if _, err := ParseToken(oldToken); err != nil {
		t.Fatalf("token signed with current key was rejected: %v", err)
	}

	// Rotate signing key
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(newKey))
	ResetKeyCacheForTest()

	if _, err := ParseToken(oldToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for old-key token after rotation, got %v", err)
	}

	newToken, _, err := GenerateToken("fixture-user", "bob", "bob@example.com", "", "user", 2)
	if err != nil {
		t.Fatalf("GenerateToken with new key failed: %v", err)
	}
	parsed, err := ParseToken(newToken)
	if err != nil || parsed.UID != "fixture-user" || parsed.TokenVersion != 2 {
		t.Fatalf("failed to parse token after rotation: claims=%+v err=%v", parsed, err)
	}

	// Reject empty UID at generation and parse time
	if _, _, err := GenerateToken("", "bob", "bob@example.com", "", "user", 1); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("expected empty UID to be rejected on generation, got %v", err)
	}
	emptyUIDClaims := &Claims{
		UID:          "",
		TokenVersion: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "jti-empty",
			Issuer:    auth.DefaultIssuer,
			Audience:  jwt.ClaimStrings{auth.DefaultAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	rawEmptyUID, err := jwt.NewWithClaims(jwt.SigningMethodHS256, emptyUIDClaims).SignedString(newKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseToken(rawEmptyUID); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("expected empty UID token to be rejected, got %v", err)
	}

	// Reject wrong issuer / audience / missing expiration
	badIssClaims := &Claims{
		UID:          "fixture-user",
		TokenVersion: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "jti-bad-iss",
			Issuer:    "other-issuer",
			Audience:  jwt.ClaimStrings{auth.DefaultAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	rawBadIss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, badIssClaims).SignedString(newKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseToken(rawBadIss); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("expected wrong issuer token to be rejected, got %v", err)
	}

	noExpClaims := &Claims{
		UID:          "fixture-user",
		TokenVersion: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:       "jti-no-exp",
			Issuer:   auth.DefaultIssuer,
			Audience: jwt.ClaimStrings{auth.DefaultAudience},
		},
	}
	rawNoExp, err := jwt.NewWithClaims(jwt.SigningMethodHS256, noExpClaims).SignedString(newKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseToken(rawNoExp); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("expected missing exp token to be rejected, got %v", err)
	}
}

func TestJWTSigningKeyRejectsInvalidConfiguration(t *testing.T) {
	t.Cleanup(ResetKeyCacheForTest)
	for _, value := range []string{"", "not-base64!", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		t.Setenv("JWT_SIGNING_KEY", value)
		ResetKeyCacheForTest()
		if err := ValidateJWTSigningKey(); !errors.Is(err, auth.ErrInvalidSigningKey) {
			t.Fatalf("startup accepted a missing or invalid key %q: got %v", value, err)
		}
		if _, _, err := GenerateToken("uid", "", "", "", "user", 1); !errors.Is(err, auth.ErrInvalidSigningKey) {
			t.Fatalf("issuer accepted a missing or invalid key %q: got %v", value, err)
		}
	}
}
