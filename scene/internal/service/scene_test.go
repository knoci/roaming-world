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
	"github.com/knoci/roaming-world/pkg/auth"
	pb "github.com/knoci/roaming-world/scene/api/scene/v1"
	"github.com/knoci/roaming-world/scene/internal/biz"
	gmetadata "google.golang.org/grpc/metadata"
)

type spySceneRepo struct {
	lastListPage       int
	lastListPageSize   int
	lastSearchKeyword  string
	lastSearchPage     int
	lastSearchPageSize int
}

func (r *spySceneRepo) CreateScene(_ context.Context, s *biz.Scene) (*biz.Scene, error) {
	return &biz.Scene{
		SID:       "sid-1",
		Name:      s.Name,
		Describe:  s.Describe,
		View:      s.View,
		Location:  s.Location,
		Article:   s.Article,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (r *spySceneRepo) DeleteScene(_ context.Context, _ string) error {
	return nil
}

func (r *spySceneRepo) UpdateScene(_ context.Context, s *biz.Scene) (*biz.Scene, error) {
	return &biz.Scene{
		SID:       s.SID,
		Name:      s.Name,
		Describe:  s.Describe,
		View:      s.View,
		Location:  s.Location,
		Article:   s.Article,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (r *spySceneRepo) SearchScene(_ context.Context, keyword string, page int, pagesize int) ([]*biz.Scene, int, error) {
	r.lastSearchKeyword = keyword
	r.lastSearchPage = page
	r.lastSearchPageSize = pagesize
	return []*biz.Scene{}, 0, nil
}

func (r *spySceneRepo) ListScenes(_ context.Context, page int, pagesize int) ([]*biz.Scene, int, error) {
	r.lastListPage = page
	r.lastListPageSize = pagesize
	return []*biz.Scene{}, 0, nil
}

func (r *spySceneRepo) GetSceneByID(_ context.Context, sid string) (*biz.Scene, error) {
	return &biz.Scene{SID: sid, Name: "Existing", CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
}

func makeTestSceneToken(t *testing.T, uid, role string) string {
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

func TestScenePaginationBounds(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	repo := &spySceneRepo{}
	uc := biz.NewSceneUsecase(repo, logger)
	svc := NewSceneService(uc, logger)

	// Service ListScenes: pagesize=0 -> 20, page=0 -> 1
	if _, err := svc.ListScenes(context.Background(), &pb.ListScenesRequest{Page: 0, Pagesize: 0}); err != nil {
		t.Fatalf("ListScenes failed: %v", err)
	}
	if repo.lastListPage != 1 || repo.lastListPageSize != 20 {
		t.Fatalf("expected ListScenes (1, 20), got (%d, %d)", repo.lastListPage, repo.lastListPageSize)
	}

	// Service ListScenes: pagesize=999999 -> 100
	if _, err := svc.ListScenes(context.Background(), &pb.ListScenesRequest{Page: -3, Pagesize: 999999}); err != nil {
		t.Fatalf("ListScenes failed: %v", err)
	}
	if repo.lastListPage != 1 || repo.lastListPageSize != 100 {
		t.Fatalf("expected ListScenes (1, 100), got (%d, %d)", repo.lastListPage, repo.lastListPageSize)
	}

	// Service SearchScene: pagesize=0 -> 20
	if _, err := svc.SearchScene(context.Background(), &pb.SearchSceneRequest{Keyword: "westlake", Page: 0, Pagesize: 0}); err != nil {
		t.Fatalf("SearchScene failed: %v", err)
	}
	if repo.lastSearchPage != 1 || repo.lastSearchPageSize != 20 {
		t.Fatalf("expected SearchScene (1, 20), got (%d, %d)", repo.lastSearchPage, repo.lastSearchPageSize)
	}

	// Service SearchScene: pagesize=999999 -> 100
	if _, err := svc.SearchScene(context.Background(), &pb.SearchSceneRequest{Keyword: "westlake", Page: 2, Pagesize: 999999}); err != nil {
		t.Fatalf("SearchScene failed: %v", err)
	}
	if repo.lastSearchPage != 2 || repo.lastSearchPageSize != 100 {
		t.Fatalf("expected SearchScene (2, 100), got (%d, %d)", repo.lastSearchPage, repo.lastSearchPageSize)
	}

	// Direct usecase enforcement as well
	if _, _, err := uc.ListScenes(context.Background(), 0, 999999); err != nil {
		t.Fatalf("uc.ListScenes failed: %v", err)
	}
	if repo.lastListPage != 1 || repo.lastListPageSize != 100 {
		t.Fatalf("expected uc.ListScenes (1, 100), got (%d, %d)", repo.lastListPage, repo.lastListPageSize)
	}
	if _, _, err := uc.SearchScene(context.Background(), "lake", 0, 0); err != nil {
		t.Fatalf("uc.SearchScene failed: %v", err)
	}
	if repo.lastSearchPage != 1 || repo.lastSearchPageSize != 20 {
		t.Fatalf("expected uc.SearchScene (1, 20), got (%d, %d)", repo.lastSearchPage, repo.lastSearchPageSize)
	}
}

func TestSceneAuth(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	repo := &spySceneRepo{}
	svc := NewSceneService(biz.NewSceneUsecase(repo, logger), logger)

	const ua = "scene-test-ua"
	createReq := &pb.CreateSceneRequest{Name: "West Lake", Describe: "Scenic lake", Location: "Hangzhou"}
	updateReq := &pb.UpdateSceneRequest{Sid: "sid-1", Name: "Updated Lake"}
	deleteReq := &pb.DeleteSceneRequest{Sid: "sid-1"}

	t.Run("anonymous returns 401", func(t *testing.T) {
		_, err := svc.CreateScene(context.Background(), createReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 401 {
			t.Fatalf("expected 401, got %v", err)
		}
		_, err = svc.UpdateScene(context.Background(), updateReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 401 {
			t.Fatalf("expected 401, got %v", err)
		}
		_, err = svc.DeleteScene(context.Background(), deleteReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 401 {
			t.Fatalf("expected 401, got %v", err)
		}
	})

	t.Run("empty uid rejected with 401", func(t *testing.T) {
		tok := makeTestSceneToken(t, "", "admin")
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs(
			"authorization", "Bearer "+tok,
			"user-agent", ua,
		))
		_, err := svc.CreateScene(ctx, createReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 401 {
			t.Fatalf("expected 401 for empty uid, got %v", err)
		}
	})

	t.Run("non-admin returns 403", func(t *testing.T) {
		tok := makeTestSceneToken(t, "user-1", "user")
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs(
			"authorization", "Bearer "+tok,
			"user-agent", ua,
		))
		_, err := svc.CreateScene(ctx, createReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 403 {
			t.Fatalf("expected 403, got %v", err)
		}
		_, err = svc.UpdateScene(ctx, updateReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 403 {
			t.Fatalf("expected 403, got %v", err)
		}
		_, err = svc.DeleteScene(ctx, deleteReq)
		if se := kerrors.FromError(err); se == nil || se.Code != 403 {
			t.Fatalf("expected 403, got %v", err)
		}
	})

	t.Run("admin via grpc incoming metadata succeeds", func(t *testing.T) {
		tok := makeTestSceneToken(t, "admin-1", "admin")
		ctx := gmetadata.NewIncomingContext(context.Background(), gmetadata.Pairs(
			"authorization", "Bearer "+tok,
			"user-agent", ua,
		))
		reply, err := svc.CreateScene(ctx, createReq)
		if err != nil || reply.Scene == nil || reply.Scene.Sid != "sid-1" {
			t.Fatalf("expected admin CreateScene success, got reply=%v err=%v", reply, err)
		}
	})

	t.Run("admin via kratos server metadata succeeds", func(t *testing.T) {
		tok := makeTestSceneToken(t, "admin-1", "admin")
		ctx := kmetadata.NewServerContext(context.Background(), kmetadata.New(map[string][]string{
			"authorization": {"Bearer " + tok},
			"user-agent":    {ua},
		}))
		if _, err := svc.DeleteScene(ctx, deleteReq); err != nil {
			t.Fatalf("expected admin DeleteScene success, got err=%v", err)
		}
	})
}
