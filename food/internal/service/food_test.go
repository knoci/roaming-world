package service

import (
	"context"
	"encoding/base64"
	"io"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	kmetadata "github.com/go-kratos/kratos/v2/metadata"
	jwt "github.com/golang-jwt/jwt/v5"
	pb "github.com/knoci/roaming-world/food/api/food/v1"
	"github.com/knoci/roaming-world/food/internal/biz"
	"github.com/knoci/roaming-world/pkg/auth"
	gmetadata "google.golang.org/grpc/metadata"
)

type spyFoodRepo struct {
	lastPage  int
	lastLimit int
}

func (r *spyFoodRepo) CreateFood(_ context.Context, f *biz.Food) (*biz.Food, error) {
	return &biz.Food{
		FID:       "fid-1",
		Name:      f.Name,
		View:      f.View,
		Describe:  f.Describe,
		Recipe:    f.Recipe,
		Article:   f.Article,
		Location:  f.Location,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (r *spyFoodRepo) GetFoodList(_ context.Context, page, limit int) ([]*biz.Food, int64, error) {
	r.lastPage = page
	r.lastLimit = limit
	return []*biz.Food{}, 0, nil
}

func (r *spyFoodRepo) GetRandomFood(_ context.Context) (*biz.Food, error) {
	return nil, nil
}

func makeTestFoodToken(t *testing.T, uid, role string) string {
	t.Helper()
	rawKey := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(rawKey))
	auth.ResetKeyCacheForTest()
	t.Cleanup(auth.ResetKeyCacheForTest)

	if uid == "" {
		now := time.Now()
		claims := &auth.Claims{
			UID:  "",
			Name: "tester",
			Role: role,
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    auth.DefaultIssuer,
				Audience:  jwt.ClaimStrings{auth.DefaultAudience},
				IssuedAt:  jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			},
		}
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(rawKey)
		if err != nil {
			t.Fatalf("sign empty-uid jwt: %v", err)
		}
		return signed
	}

	tok, _, err := auth.GenerateAccessToken(uid, "tester", "", "", role, 1, time.Hour)
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}
	return tok
}

func TestFoodPaginationBounds(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	repo := &spyFoodRepo{}
	svc := NewFoodService(biz.NewFoodUsecase(repo, logger), logger)

	// Default page=0, limit=0 -> page=1, limit=20
	if _, err := svc.GetFoodList(context.Background(), &pb.GetFoodListRequest{Page: 0, Limit: 0}); err != nil {
		t.Fatalf("GetFoodList failed: %v", err)
	}
	if repo.lastPage != 1 || repo.lastLimit != 20 {
		t.Fatalf("expected (1, 20), got (%d, %d)", repo.lastPage, repo.lastLimit)
	}

	// Negative page/limit -> page=1, limit=20
	if _, err := svc.GetFoodList(context.Background(), &pb.GetFoodListRequest{Page: -2, Limit: -5}); err != nil {
		t.Fatalf("GetFoodList failed: %v", err)
	}
	if repo.lastPage != 1 || repo.lastLimit != 20 {
		t.Fatalf("expected (1, 20), got (%d, %d)", repo.lastPage, repo.lastLimit)
	}

	// limit=9999 -> clamped to 100
	if _, err := svc.GetFoodList(context.Background(), &pb.GetFoodListRequest{Page: 3, Limit: 9999}); err != nil {
		t.Fatalf("GetFoodList failed: %v", err)
	}
	if repo.lastPage != 3 || repo.lastLimit != 100 {
		t.Fatalf("expected (3, 100), got (%d, %d)", repo.lastPage, repo.lastLimit)
	}
}

func TestFoodAuth(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	repo := &spyFoodRepo{}
	svc := NewFoodService(biz.NewFoodUsecase(repo, logger), logger)

	const ua = "food-test-ua"
	req := &pb.CreateFoodRequest{
		Name:     "Hotpot",
		View:     []string{"https://example.com/hotpot.jpg"},
		Describe: "Spicy",
		Recipe:   "Boil",
		Article:  "Sichuan classic",
		Location: "Chengdu",
	}

	t.Run("anonymous returns 401", func(t *testing.T) {
		_, err := svc.CreateFood(context.Background(), req)
		if se := kerrors.FromError(err); se == nil || se.Code != 401 {
			t.Fatalf("expected 401, got %v", err)
		}
	})

	t.Run("empty uid rejected with 401", func(t *testing.T) {
		tok := makeTestFoodToken(t, "", "admin")
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs(
			"authorization", "Bearer "+tok,
			"user-agent", ua,
		))
		_, err := svc.CreateFood(ctx, req)
		if se := kerrors.FromError(err); se == nil || se.Code != 401 {
			t.Fatalf("expected 401 for empty uid, got %v", err)
		}
	})

	t.Run("non-admin returns 403", func(t *testing.T) {
		tok := makeTestFoodToken(t, "user-1", "user")
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs(
			"authorization", "Bearer "+tok,
			"user-agent", ua,
		))
		_, err := svc.CreateFood(ctx, req)
		if se := kerrors.FromError(err); se == nil || se.Code != 403 {
			t.Fatalf("expected 403, got %v", err)
		}
	})

	t.Run("admin via grpc incoming metadata succeeds", func(t *testing.T) {
		tok := makeTestFoodToken(t, "admin-1", "admin")
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs(
			"authorization", "Bearer "+tok,
			"user-agent", ua,
		))
		reply, err := svc.CreateFood(ctx, req)
		if err != nil || reply.Food == nil || reply.Food.Fid != "fid-1" {
			t.Fatalf("expected admin CreateFood success, got reply=%v err=%v", reply, err)
		}
	})

	t.Run("admin via kratos server metadata succeeds", func(t *testing.T) {
		tok := makeTestFoodToken(t, "admin-1", "admin")
		ctx := kmetadata.NewServerContext(context.Background(), kmetadata.New(map[string][]string{
			"authorization": {"Bearer " + tok},
			"user-agent":    {ua},
		}))
		reply, err := svc.CreateFood(ctx, req)
		if err != nil || reply.Food == nil || reply.Food.Fid != "fid-1" {
			t.Fatalf("expected admin CreateFood success via kratos metadata, got reply=%v err=%v", reply, err)
		}
	})
}
