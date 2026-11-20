package data

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	pb "github.com/knoci/roaming-world/article/api/article/v1"
	"github.com/knoci/roaming-world/article/internal/biz"
	"github.com/knoci/roaming-world/article/internal/service"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Both callers see the same initial state even if one write completes first.
type articleSnapshotRepo struct {
	biz.ArticleRepo
	snapshot *biz.Article
}

func (r articleSnapshotRepo) FindByID(context.Context, string) (*biz.Article, error) {
	snapshot := *r.snapshot
	return &snapshot, nil
}

func TestArticleLifecyclePostgres(t *testing.T) {
	dsn := os.Getenv("ROAMING_TEST_ARTICLE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ROAMING_TEST_ARTICLE_POSTGRES_DSN to a migrated test database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger := log.NewStdLogger(io.Discard)
	repo := NewArticleRepo(&Data{db: db, log: log.NewHelper(logger)}, logger)
	uc := biz.NewArticleUsecase(repo, logger)
	const owner = "fixture-owner"
	create := func(t *testing.T) *biz.Article {
		t.Helper()
		row, err := uc.Create(ctx, owner, "Author", "", &pb.CreateArticleRequest{Title: "Draft", Content: "Initial content", View: []string{"original.jpg"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := repo.Delete(context.Background(), row.AID); err != nil {
				t.Error(err)
			}
		})
		return row
	}
	load := func(t *testing.T, aid string) *biz.Article {
		t.Helper()
		row, err := repo.FindByID(ctx, aid)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}

	t.Run("editing cannot write over a submitted revision", func(t *testing.T) {
		draft := create(t)
		stale := biz.NewArticleUsecase(articleSnapshotRepo{repo, draft}, logger)
		if _, err := uc.Submit(ctx, owner, draft.AID); err != nil {
			t.Fatal(err)
		}
		if _, err := stale.Update(ctx, owner, "", &pb.UpdateArticleRequest{
			Aid: draft.AID, Title: "Late edit", Content: "Late content", View: []string{"new.jpg"},
		}); !errors.Is(err, biz.ErrArticleChanged) {
			t.Fatalf("expected the stale edit to be rejected, got %v", err)
		}
		row := load(t, draft.AID)
		if row.Status != "pending" || row.Content != draft.Content {
			t.Fatal("late edit changed the content under review")
		}
	})

	t.Run("only one review can commit per revision", func(t *testing.T) {
		draft := create(t)
		if _, err := uc.Submit(ctx, owner, draft.AID); err != nil {
			t.Fatal(err)
		}
		pending := load(t, draft.AID)
		stale := biz.NewArticleUsecase(articleSnapshotRepo{repo, pending}, logger)
		results := make(chan error, 2)
		for _, action := range []string{"approve", "reject"} {
			go func(action string) {
				_, err := stale.Review(ctx, "admin", draft.AID, action, action, &pending.Revision)
				results <- err
			}(action)
		}
		first, second := <-results, <-results
		if !((first == nil && errors.Is(second, biz.ErrArticleChanged)) ||
			(second == nil && errors.Is(first, biz.ErrArticleChanged))) {
			t.Fatalf("expected a decision and a conflict, got %v and %v", first, second)
		}
		row := load(t, draft.AID)
		if row.Revision != pending.Revision+1 {
			t.Fatalf("expected a single committed transition, got revision %d", row.Revision)
		}
	})

	t.Run("reload before reviewing a resubmission", func(t *testing.T) {
		draft := create(t)
		if _, err := uc.Submit(ctx, owner, draft.AID); err != nil {
			t.Fatal(err)
		}
		pending := load(t, draft.AID)
		if _, err := uc.Review(ctx, "admin", draft.AID, "reject", "revise", &pending.Revision); err != nil {
			t.Fatal(err)
		}
		updated, err := uc.Update(ctx, owner, "", &pb.UpdateArticleRequest{
			Aid: draft.AID, Title: "Revised", Content: "Revised content", View: []string{"new.jpg"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if updated.Title != "Revised" || len(updated.View) != 1 || updated.View[0] != "new.jpg" {
			t.Fatalf("unexpected edit response: %+v", updated)
		}
		if _, err := uc.Submit(ctx, owner, draft.AID); err != nil {
			t.Fatal(err)
		}
		if _, err := uc.Review(ctx, "admin", draft.AID, "approve", "", &pending.Revision); !errors.Is(err, biz.ErrArticleChanged) {
			t.Fatalf("stale reviewer could approve a different revision: %v", err)
		}
		if _, err := uc.Review(ctx, "admin", draft.AID, "approve", "", nil); err != nil {
			t.Fatalf("legacy review failed: %v", err)
		}
		svc := service.NewArticleService(uc, logger)
		revisedList, err := svc.ListArticles(ctx, &pb.ListArticlesRequest{Way: "time", Reverse: true, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if revisedList.Total != 1 || len(revisedList.Articles) != 1 || revisedList.Articles[0].Aid != draft.AID {
			t.Fatalf("published article missing from public list: %+v", revisedList)
		}
		if _, err := uc.Review(ctx, "admin", draft.AID, "offline", "", nil); err != nil {
			t.Fatal(err)
		}
		offlineList, err := svc.ListArticles(ctx, &pb.ListArticlesRequest{Way: "time", Reverse: true, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if offlineList.Total != 0 || len(offlineList.Articles) != 0 {
			t.Fatalf("public list still includes an offline article: %+v", offlineList)
		}
		if _, err := svc.GetArticle(ctx, &pb.GetArticleRequest{Aid: draft.AID}); !errors.Is(err, biz.ErrArticleNotFound) {
			t.Fatalf("offline article is still public: %v", err)
		}
	})
}
