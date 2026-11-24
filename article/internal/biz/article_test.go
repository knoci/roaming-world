package biz

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
)

type stubArticleRepo struct {
	articles        map[string]*Article
	lastPage        int
	lastLimit       int
	unlikeCalled    bool
	unfavCalled     bool
	unlikeCount     int
	unfavCount      int
	likeCalled      bool
	favoriteCalled  bool
}

func (s *stubArticleRepo) Create(context.Context, *Article) (*Article, error) { return nil, nil }
func (s *stubArticleRepo) Update(context.Context, *Article) (*Article, error) { return nil, nil }
func (s *stubArticleRepo) Delete(context.Context, string) error               { return nil }

func (s *stubArticleRepo) FindByID(_ context.Context, aid string) (*Article, error) {
	a, ok := s.articles[aid]
	if !ok {
		return nil, errors.New("not found")
	}
	return a, nil
}

func (s *stubArticleRepo) List(_ context.Context, page, limit int, _ string, _ bool, _, _ string) ([]*Article, int64, error) {
	s.lastPage = page
	s.lastLimit = limit
	return []*Article{}, 0, nil
}

func (s *stubArticleRepo) ListFeed(context.Context, string, int, int) ([]*Article, int64, error) {
	return []*Article{}, 0, nil
}
func (s *stubArticleRepo) UpdateStatus(context.Context, *Article, string, string, string) error {
	return nil
}

func (s *stubArticleRepo) Like(context.Context, string, string) (bool, int, error) {
	s.likeCalled = true
	return true, 1, nil
}

func (s *stubArticleRepo) Unlike(context.Context, string, string) (int, error) {
	s.unlikeCalled = true
	return s.unlikeCount, nil
}

func (s *stubArticleRepo) Favorite(context.Context, string, string) (bool, int, error) {
	s.favoriteCalled = true
	return true, 1, nil
}

func (s *stubArticleRepo) Unfavorite(context.Context, string, string) (int, error) {
	s.unfavCalled = true
	return s.unfavCount, nil
}

func (s *stubArticleRepo) GetUserLikeList(context.Context, string) ([]string, error)     { return nil, nil }
func (s *stubArticleRepo) GetUserFavoriteList(context.Context, string) ([]string, error) { return nil, nil }
func (s *stubArticleRepo) GetVideoArticle(context.Context, int) (*Article, error)        { return nil, nil }
func (s *stubArticleRepo) GetMaxVideoID(context.Context) (int, error)                    { return 0, nil }

func TestListClampsPaginationBounds(t *testing.T) {
	repo := &stubArticleRepo{}
	uc := NewArticleUsecase(repo, log.NewStdLogger(io.Discard))

	cases := []struct {
		name      string
		inPage    int
		inLimit   int
		wantPage  int
		wantLimit int
	}{
		{name: "limit zero defaults to 20", inPage: 0, inLimit: 0, wantPage: 1, wantLimit: 20},
		{name: "negative page and limit clamped", inPage: -3, inLimit: -5, wantPage: 1, wantLimit: 20},
		{name: "huge limit clamped to 50", inPage: 2, inLimit: 999, wantPage: 2, wantLimit: 50},
		{name: "valid bounds preserved", inPage: 3, inLimit: 25, wantPage: 3, wantLimit: 25},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := uc.List(context.Background(), tc.inPage, tc.inLimit, "time", true, "published", ""); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo.lastPage != tc.wantPage || repo.lastLimit != tc.wantLimit {
				t.Fatalf("got page=%d limit=%d, want page=%d limit=%d", repo.lastPage, repo.lastLimit, tc.wantPage, tc.wantLimit)
			}
		})
	}
}

func TestUnlikeAndUnfavoriteAllowOfflineArticle(t *testing.T) {
	for _, status := range []string{"offline", "draft", "rejected"} {
		t.Run("status="+status, func(t *testing.T) {
			repo := &stubArticleRepo{
				articles: map[string]*Article{
					"aid-1": {AID: "aid-1", Status: status},
				},
				unlikeCount: 3,
				unfavCount:  2,
			}
			uc := NewArticleUsecase(repo, log.NewStdLogger(io.Discard))

			likes, err := uc.Unlike(context.Background(), "u1", "aid-1")
			if err != nil {
				t.Fatalf("expected Unlike to succeed on %s article, got %v", status, err)
			}
			if !repo.unlikeCalled || likes != 3 {
				t.Fatalf("expected repo.Unlike called with count=3, called=%v count=%d", repo.unlikeCalled, likes)
			}

			favs, err := uc.Unfavorite(context.Background(), "u1", "aid-1")
			if err != nil {
				t.Fatalf("expected Unfavorite to succeed on %s article, got %v", status, err)
			}
			if !repo.unfavCalled || favs != 2 {
				t.Fatalf("expected repo.Unfavorite called with count=2, called=%v count=%d", repo.unfavCalled, favs)
			}

			// Liking/favoriting a non-published article must still be rejected.
			if _, _, err := uc.Like(context.Background(), "u1", "aid-1"); !errors.Is(err, ErrArticleNotFound) {
				t.Fatalf("expected Like on %s article to fail with ErrArticleNotFound, got %v", status, err)
			}
			if _, _, err := uc.Favorite(context.Background(), "u1", "aid-1"); !errors.Is(err, ErrArticleNotFound) {
				t.Fatalf("expected Favorite on %s article to fail with ErrArticleNotFound, got %v", status, err)
			}
		})
	}

	t.Run("missing article returns ErrArticleNotFound", func(t *testing.T) {
		repo := &stubArticleRepo{articles: map[string]*Article{}}
		uc := NewArticleUsecase(repo, log.NewStdLogger(io.Discard))

		if _, err := uc.Unlike(context.Background(), "u1", "aid-missing"); !errors.Is(err, ErrArticleNotFound) {
			t.Fatalf("expected ErrArticleNotFound, got %v", err)
		}
		if _, err := uc.Unfavorite(context.Background(), "u1", "aid-missing"); !errors.Is(err, ErrArticleNotFound) {
			t.Fatalf("expected ErrArticleNotFound, got %v", err)
		}
	})
}
