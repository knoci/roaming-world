package pkg

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTSigningKeyRotation(t *testing.T) {
	const ua = "rotation-test-client"
	oldKey := bytes.Repeat([]byte{1}, 32)
	newKey := bytes.Repeat([]byte{2}, 32)
	claims := Claims{
		UID: "fixture-user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	sign := func(key []byte, claims Claims) string {
		t.Helper()
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		token, err := encryptToken(signed, ua)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}

	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(oldKey))
	oldToken := sign(oldKey, claims)
	if _, err := ParseToken(ua, oldToken); err != nil {
		t.Fatalf("token signed with the current key was rejected: %v", err)
	}

	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(newKey))
	if _, err := ParseToken(ua, oldToken); !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		t.Fatalf("expected the previous token's signature to be rejected, got %v", err)
	}
	current, err := ParseToken(ua, sign(newKey, claims))
	if err != nil || current.UID != claims.UID {
		t.Fatalf("could not read the token signed after rotation: %v", err)
	}
	claims.ExpiresAt = nil
	if _, err := ParseToken(ua, sign(newKey, claims)); !errors.Is(err, jwt.ErrTokenRequiredClaimMissing) {
		t.Fatalf("expected an expiration to be required, got %v", err)
	}
}

func TestJWTSigningKeyRejectsInvalidConfiguration(t *testing.T) {
	for _, value := range []string{"", "not-base64!", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		t.Setenv("JWT_SIGNING_KEY", value)
		if err := ValidateJWTSigningKey(); err == nil {
			t.Fatal("startup accepted a missing or invalid key")
		}
		if _, err := GenerateToken("fixture", "uid", "", "", "", "user"); err == nil {
			t.Fatal("issuer accepted a missing or invalid key")
		}
	}
}
