package service

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/knoci/roaming-world/pkg/auth"
	pb "github.com/knoci/roaming-world/plan/api/plan/v1"
	"github.com/knoci/roaming-world/plan/internal/biz"
	grpcmd "google.golang.org/grpc/metadata"
)

type stubRepo struct {
	plan  *biz.Plan
	plans []*biz.Plan
}

func (s *stubRepo) Create(_ context.Context, p *biz.Plan) (*biz.Plan, error) { return p, nil }
func (s *stubRepo) FindByID(_ context.Context, _ string) (*biz.Plan, error) {
	if s.plan == nil {
		return nil, biz.ErrPlanNotFound
	}
	return s.plan, nil
}
func (s *stubRepo) List(_ context.Context, _ string, _ string, _, _ int, _ bool) ([]*biz.Plan, int64, error) {
	return s.plans, int64(len(s.plans)), nil
}
func (s *stubRepo) Update(_ context.Context, p *biz.Plan) (*biz.Plan, error) { return p, nil }
func (s *stubRepo) Publish(_ context.Context, _, _ string) error             { return nil }
func (s *stubRepo) AddItem(_ context.Context, _ string, _ int, _ *biz.Item) (*biz.Plan, error) {
	return s.plan, nil
}
func (s *stubRepo) DeleteItem(_ context.Context, _, _, _ string) error { return nil }
func (s *stubRepo) Delete(_ context.Context, _, _ string) error        { return nil }

func setupTestJWTKey(t *testing.T) []byte {
	t.Helper()
	auth.ResetKeyCacheForTest()
	t.Cleanup(auth.ResetKeyCacheForTest)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(key))
	return key
}

func mintStandardToken(t *testing.T, uid, role string) string {
	t.Helper()
	tok, _, err := auth.GenerateAccessToken(uid, "Test User", "test@example.com", "", role, 1, time.Hour)
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}
	return tok
}

func mintRawToken(t *testing.T, key []byte, claims *auth.Claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign raw token: %v", err)
	}
	return signed
}

func httpTestServerWithPlan(svc *PlanService) *khttp.Server {
	srv := khttp.NewServer()
	pb.RegisterPlanHTTPServer(srv, svc)
	return srv
}

func TestPlanService_GetAndListAuthHandling(t *testing.T) {
	setupTestJWTKey(t)
	logger := log.NewStdLogger(io.Discard)
	pubPlan := &biz.Plan{
		ID:         "plan-public",
		UID:        "owner-1",
		Title:      "Public Trip",
		City:       "Chengdu",
		StartDate:  time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:    time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC),
		Visibility: "public",
		Status:     "published",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	uc := biz.NewPlanUsecase(&stubRepo{plan: pubPlan, plans: []*biz.Plan{pubPlan}}, logger)
	svc := NewPlanService(uc, logger)
	srv := httpTestServerWithPlan(svc)

	t.Run("GetPlan allows missing Authorization header for public plan", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(stdhttp.MethodGet, "/v1/plan/plan-public", nil)
		srv.ServeHTTP(rec, req)
		if rec.Code != stdhttp.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GetPlan returns 401 when Authorization header is invalid", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(stdhttp.MethodGet, "/v1/plan/plan-public", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		srv.ServeHTTP(rec, req)
		if rec.Code != stdhttp.StatusUnauthorized {
			t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("ListPlans allows missing Authorization header for public scope", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(stdhttp.MethodGet, "/v1/plan?scope=public", nil)
		srv.ServeHTTP(rec, req)
		if rec.Code != stdhttp.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("ListPlans returns 401 when Authorization header is invalid", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(stdhttp.MethodGet, "/v1/plan?scope=public", nil)
		req.Header.Set("Authorization", "Bearer malformed.jwt.token")
		srv.ServeHTTP(rec, req)
		if rec.Code != stdhttp.StatusUnauthorized {
			t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GetPlan accepts valid standard HS256 Authorization header without User-Agent", func(t *testing.T) {
		tok := mintStandardToken(t, "owner-1", "user")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(stdhttp.MethodGet, "/v1/plan/plan-public", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		srv.ServeHTTP(rec, req)
		if rec.Code != stdhttp.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestPlanService_GRPCMetadataAuth(t *testing.T) {
	key := setupTestJWTKey(t)
	logger := log.NewStdLogger(io.Discard)
	uc := biz.NewPlanUsecase(&stubRepo{}, logger)
	svc := NewPlanService(uc, logger)

	t.Run("valid gRPC incoming metadata succeeds without User-Agent", func(t *testing.T) {
		tok := mintStandardToken(t, "user-grpc-1", "user")
		ctx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
			"authorization", "Bearer "+tok,
		))
		reply, err := svc.CreatePlan(ctx, &pb.CreatePlanRequest{
			Title:     "gRPC Plan",
			City:      "Beijing",
			StartDate: "2030-06-01",
			EndDate:   "2030-06-02",
			Budget:    100,
		})
		if err != nil {
			t.Fatalf("expected success with gRPC metadata, got %v", err)
		}
		if reply.GetPlan().GetUid() != "user-grpc-1" {
			t.Fatalf("expected uid user-grpc-1, got %q", reply.GetPlan().GetUid())
		}
	})

	t.Run("gRPC token with empty UID is rejected with 401", func(t *testing.T) {
		tok := mintRawToken(t, key, &auth.Claims{
			UID:  "",
			Role: "user",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    auth.DefaultIssuer,
				Audience:  jwt.ClaimStrings{auth.DefaultAudience},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		})
		ctx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
			"authorization", "Bearer "+tok,
		))
		_, err := svc.CreatePlan(ctx, &pb.CreatePlanRequest{
			Title:     "gRPC Plan",
			City:      "Beijing",
			StartDate: "2030-06-01",
			EndDate:   "2030-06-02",
		})
		if !errors.Is(err, biz.ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got %v", err)
		}
		if ke := kerrors.FromError(err); ke.Code != 401 {
			t.Fatalf("expected 401, got %d", ke.Code)
		}
	})
}
