package data

import (
	"bytes"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/knoci/roaming-world/user/internal/biz"
)

func TestVerificationKeysNormalizeEmail(t *testing.T) {
	t1 := biz.VerificationTarget{Email: "  Alice.User@Example.COM ", Purpose: biz.VerificationRegister, UID: "u1"}
	t2 := biz.VerificationTarget{Email: "alice.user@example.com", Purpose: biz.VerificationRegister, UID: "u1"}

	if verificationKey(t1) != verificationKey(t2) {
		t.Fatalf("verificationKey not normalized: %s vs %s", verificationKey(t1), verificationKey(t2))
	}
	if verificationCooldownKey(t1.Email) != verificationCooldownKey(t2.Email) {
		t.Fatalf("verificationCooldownKey not normalized: %s vs %s", verificationCooldownKey(t1.Email), verificationCooldownKey(t2.Email))
	}
}

func TestMapUniqueViolation(t *testing.T) {
	emailErr := &pgconn.PgError{Code: "23505", ConstraintName: "uk_users_email"}
	if got := mapUniqueViolation(emailErr); !errors.Is(got, biz.ErrEmailAlreadyExists) {
		t.Fatalf("expected ErrEmailAlreadyExists, got %v", got)
	}

	nameErr := &pgconn.PgError{Code: "23505", ConstraintName: "uk_users_name"}
	if got := mapUniqueViolation(nameErr); !errors.Is(got, biz.ErrUsernameAlreadyExists) {
		t.Fatalf("expected ErrUsernameAlreadyExists, got %v", got)
	}

	otherErr := &pgconn.PgError{Code: "22P02", ConstraintName: "uk_users_email"}
	if got := mapUniqueViolation(otherErr); got != nil {
		t.Fatalf("expected nil for non-23505 error, got %v", got)
	}
}

func TestExtractCOSObjectKey(t *testing.T) {
	key, ok := extractCOSObjectKey("https://bucket-123.cos.ap-shanghai.myqcloud.com/avatars/uid-1/abc.png")
	if !ok || key != "avatars/uid-1/abc.png" {
		t.Fatalf("unexpected COS key: %q, ok=%v", key, ok)
	}
	if _, ok := extractCOSObjectKey("https://bucket-123.cos.ap-shanghai.myqcloud.com/"); ok {
		t.Fatal("expected root path to be rejected")
	}
	if _, ok := extractCOSObjectKey(""); ok {
		t.Fatal("expected empty URL to be rejected")
	}
}

func TestDetectAvatarType(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	mime, ext, err := detectAvatarType(bytes.NewReader(png))
	if err != nil || mime != "image/png" || ext != ".png" {
		t.Fatalf("expected image/png .png, got mime=%q ext=%q err=%v", mime, ext, err)
	}

	html := []byte("<html><script>alert(1)</script></html>")
	if _, _, err := detectAvatarType(bytes.NewReader(html)); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for html payload, got %v", err)
	}
}
