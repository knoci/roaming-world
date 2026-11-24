package service

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware/validate"
	jwt "github.com/golang-jwt/jwt/v5"
	pb "github.com/knoci/roaming-world/article/api/article/v1"
	"github.com/knoci/roaming-world/article/internal/biz"
	"github.com/knoci/roaming-world/pkg/auth"
	grpcmd "google.golang.org/grpc/metadata"
)

func setupTestSigningKey(t *testing.T) []byte {
	t.Helper()
	signingKey := []byte("0123456789abcdef0123456789abcdef")
	auth.ResetKeyCacheForTest()
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(signingKey))
	t.Cleanup(auth.ResetKeyCacheForTest)
	return signingKey
}

func TestGetJwtClaimGRPCIncomingMetadata(t *testing.T) {
	setupTestSigningKey(t)

	token, _, err := auth.GenerateAccessToken("author-7", "Author", "author@example.com", "avatar.png", "admin", 1, time.Hour)
	if err != nil {
		t.Fatalf("GenerateAccessToken failed: %v", err)
	}

	ctx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
		"authorization", "Bearer "+token,
	))

	claims, err := GetJwtClaim(ctx)
	if err != nil {
		t.Fatalf("expected valid claims from gRPC incoming metadata, got %v", err)
	}
	if claims.UID != "author-7" || claims.Role != "admin" || claims.Name != "Author" {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	// Fallback also works when Kratos metadata exists without authorization.
	ctxWithKratosMD := metadata.NewServerContext(ctx, metadata.New(map[string][]string{"x-req": {"1"}}))
	claims, err = GetJwtClaim(ctxWithKratosMD)
	if err != nil || claims.UID != "author-7" {
		t.Fatalf("expected fallback to gRPC incoming metadata, got claims=%+v err=%v", claims, err)
	}
}

func TestGetJwtClaimRejectsEmptyUIDAndInvalidTokens(t *testing.T) {
	signingKey := setupTestSigningKey(t)

	// Missing header -> ErrUnauthorized
	if _, err := GetJwtClaim(context.Background()); !errors.Is(err, biz.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for missing token, got %v", err)
	}

	// Empty or whitespace UID in a signed HS256 JWT -> ErrUnauthorized
	for _, uid := range []string{"", "   "} {
		now := time.Now()
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, &auth.Claims{
			UID: uid,
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    auth.DefaultIssuer,
				Audience:  jwt.ClaimStrings{auth.DefaultAudience},
				IssuedAt:  jwt.NewNumericDate(now),
				NotBefore: jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			},
		})
		signed, err := tok.SignedString(signingKey)
		if err != nil {
			t.Fatalf("sign jwt: %v", err)
		}
		ctx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
			"authorization", "Bearer "+signed,
		))
		if _, err := GetJwtClaim(ctx); !errors.Is(err, biz.ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized for uid=%q, got %v", uid, err)
		}
	}

	// Expired token -> ErrUnauthorized
	expired := jwt.NewWithClaims(jwt.SigningMethodHS256, &auth.Claims{
		UID: "author-8",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    auth.DefaultIssuer,
			Audience:  jwt.ClaimStrings{auth.DefaultAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	})
	signedExpired, err := expired.SignedString(signingKey)
	if err != nil {
		t.Fatalf("sign expired jwt: %v", err)
	}
	ctxExpired := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs("authorization", "Bearer "+signedExpired))
	if _, err := GetJwtClaim(ctxExpired); !errors.Is(err, biz.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for expired token, got %v", err)
	}
}

func TestValidateMiddlewareRejectsEmptyArticleFields(t *testing.T) {
	mw := validate.Validator()
	endpoint := mw(func(ctx context.Context, req interface{}) (interface{}, error) {
		return &pb.CreateArticleReply{}, nil
	})

	_, err := endpoint(context.Background(), &pb.CreateArticleRequest{Title: "", View: []string{"a.jpg"}, Content: "body"})
	if err == nil || kerrors.Code(err) != 400 {
		t.Fatalf("expected HTTP 400 validation error for empty title, got %v", err)
	}

	_, err = endpoint(context.Background(), &pb.CreateArticleRequest{Title: "Title", View: []string{"a.jpg"}, Content: "body"})
	if err != nil {
		t.Fatalf("expected valid request to pass validation, got %v", err)
	}
}
