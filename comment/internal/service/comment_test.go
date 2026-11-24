package service

import (
	"context"
	"encoding/base64"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware/validate"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	jwt "github.com/golang-jwt/jwt/v5"
	pb "github.com/knoci/roaming-world/comment/api/comment/v1"
	"github.com/knoci/roaming-world/comment/internal/biz"
	"github.com/knoci/roaming-world/pkg/auth"
	grpcmd "google.golang.org/grpc/metadata"
)

func setupTestJWTKey(t *testing.T) []byte {
	t.Helper()
	signingKey := []byte("0123456789abcdef0123456789abcdef")
	auth.ResetKeyCacheForTest()
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(signingKey))
	t.Cleanup(auth.ResetKeyCacheForTest)
	return signingKey
}

func makeRawJWT(t *testing.T, signingKey []byte, claims auth.Claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(signingKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signed
}

func TestGetJwtClaimGRPCIncomingMetadata(t *testing.T) {
	setupTestJWTKey(t)

	token, _, err := auth.GenerateAccessToken("user-42", "Tester", "tester@example.com", "", "user", 1, time.Hour)
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}

	ctx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
		"authorization", "Bearer "+token,
	))

	claims, err := GetJwtClaim(ctx)
	if err != nil {
		t.Fatalf("expected valid claims from gRPC incoming metadata, got error: %v", err)
	}
	if claims.UID != "user-42" || claims.Name != "Tester" {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	// Fallback also works when Kratos server metadata exists without auth keys.
	ctxWithEmptyKratosMD := metadata.NewServerContext(ctx, metadata.New(map[string][]string{"x-trace": {"1"}}))
	claims, err = GetJwtClaim(ctxWithEmptyKratosMD)
	if err != nil || claims.UID != "user-42" {
		t.Fatalf("expected fallback to gRPC incoming metadata, got claims=%+v err=%v", claims, err)
	}
}

func TestGetJwtClaimHTTPHeaderWithoutUserAgent(t *testing.T) {
	setupTestJWTKey(t)

	token, _, err := auth.GenerateAccessToken("user-http", "Alice", "alice@example.com", "", "admin", 2, time.Hour)
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}

	srv := khttp.NewServer()
	var claims *auth.Claims
	var gotErr error
	srv.Route("/").POST("/v1/comment", func(ctx khttp.Context) error {
		claims, gotErr = GetJwtClaim(ctx)
		return ctx.String(nethttp.StatusOK, "ok")
	})

	req := httptest.NewRequest(nethttp.MethodPost, "/v1/comment", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if gotErr != nil {
		t.Fatalf("expected valid claims from HTTP header without UA, got error: %v", gotErr)
	}
	if claims == nil || claims.UID != "user-http" || claims.Role != "admin" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestGetJwtClaimRejectsEmptyUID(t *testing.T) {
	signingKey := setupTestJWTKey(t)

	for _, uid := range []string{"", "   "} {
		token := makeRawJWT(t, signingKey, auth.Claims{
			UID:  uid,
			Name: "NoUID",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    auth.DefaultIssuer,
				Audience:  jwt.ClaimStrings{auth.DefaultAudience},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		})
		ctx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
			"authorization", "Bearer "+token,
		))
		if _, err := GetJwtClaim(ctx); !errors.Is(err, biz.ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized for uid=%q, got %v", uid, err)
		}
	}
}

func TestValidateMiddlewareRejectsEmptyTargetOrContent(t *testing.T) {
	mw := validate.Validator()
	called := false
	endpoint := mw(func(ctx context.Context, req interface{}) (interface{}, error) {
		called = true
		return &pb.CreateCommentReply{}, nil
	})

	for _, tc := range []struct {
		name string
		req  *pb.CreateCommentRequest
	}{
		{name: "empty target", req: &pb.CreateCommentRequest{Target: "", Content: "hello"}},
		{name: "empty content", req: &pb.CreateCommentRequest{Target: "aid-1", Content: ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			_, err := endpoint(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
			if code := kerrors.Code(err); code != 400 {
				t.Fatalf("expected HTTP 400 validation error, got %d (%v)", code, err)
			}
			if called {
				t.Fatalf("handler should not be called when validation fails")
			}
		})
	}

	called = false
	if _, err := endpoint(context.Background(), &pb.CreateCommentRequest{Target: "aid-1", Content: "valid"}); err != nil {
		t.Fatalf("expected valid request to pass validation, got %v", err)
	}
	if !called {
		t.Fatal("expected handler to be called for valid request")
	}
}

func TestToMessageMasksDeletedCommentContent(t *testing.T) {
	deleted := toMessage(&biz.Comment{
		CID:     "c1",
		Content: "original audit text",
		Status:  "deleted",
	})
	if deleted.Content != deletedCommentContent {
		t.Fatalf("expected deleted comment content to be masked as %q, got %q", deletedCommentContent, deleted.Content)
	}

	active := toMessage(&biz.Comment{
		CID:     "c2",
		Content: "visible text",
		Status:  "active",
	})
	if active.Content != "visible text" {
		t.Fatalf("expected active comment content unchanged, got %q", active.Content)
	}
}
