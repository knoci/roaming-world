package biz

import (
	"context"
	"errors"
	"io"
	"testing"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
)

type stubCommentRepo struct {
	published    map[string]bool
	publishedErr error
	users        map[string]*User
	userErr      error
	comments     map[string]*Comment
	commentErr   error
	created      *Comment
	listCalled   bool
	listPage     int
	listLimit    int
	listResult   []*Comment
	listTotal    int64
}

func (s *stubCommentRepo) Create(_ context.Context, comment *Comment) (*Comment, error) {
	cp := *comment
	cp.CID = "cid-created"
	s.created = &cp
	return &cp, nil
}

func (s *stubCommentRepo) Delete(context.Context, string, string) error { return nil }
func (s *stubCommentRepo) Like(context.Context, string, string) error   { return nil }
func (s *stubCommentRepo) Unlike(context.Context, string, string) error { return nil }

func (s *stubCommentRepo) FindComment(_ context.Context, cid string) (*Comment, error) {
	if s.commentErr != nil {
		return nil, s.commentErr
	}
	c, ok := s.comments[cid]
	if !ok {
		return nil, ErrCommentNotFound
	}
	return c, nil
}

func (s *stubCommentRepo) IsArticlePublished(_ context.Context, aid string) (bool, error) {
	if s.publishedErr != nil {
		return false, s.publishedErr
	}
	return s.published[aid], nil
}

func (s *stubCommentRepo) GetCommentList(_ context.Context, _ string, page, limit int) ([]*Comment, int64, error) {
	s.listCalled = true
	s.listPage = page
	s.listLimit = limit
	return s.listResult, s.listTotal, nil
}

func (s *stubCommentRepo) GetReplyList(context.Context, string, int, int) ([]*Comment, int64, error) {
	return nil, 0, nil
}

func (s *stubCommentRepo) GetCommentListWithReplies(_ context.Context, _ string, page, limit int) ([]*Comment, int64, error) {
	s.listCalled = true
	s.listPage = page
	s.listLimit = limit
	return s.listResult, s.listTotal, nil
}

func (s *stubCommentRepo) CreateReport(context.Context, *CommentReport) (*CommentReport, error) {
	return nil, nil
}
func (s *stubCommentRepo) ListReports(context.Context, string, int, int) ([]*CommentReport, int64, error) {
	return nil, 0, nil
}
func (s *stubCommentRepo) ResolveReport(context.Context, string, string, string, string) (*CommentReport, error) {
	return nil, nil
}

func (s *stubCommentRepo) FindUser(_ context.Context, uid string) (*User, error) {
	if s.userErr != nil {
		return nil, s.userErr
	}
	u, ok := s.users[uid]
	if !ok {
		return nil, ErrUserNotFound
	}
	return u, nil
}

func TestCreateCommentRejectsUnpublishedArticle(t *testing.T) {
	repo := &stubCommentRepo{
		published: map[string]bool{"aid-pub": true},
		users:     map[string]*User{"u1": {UID: "u1", Name: "Alice"}},
	}
	uc := NewCommentUsecase(repo, log.NewStdLogger(io.Discard))

	_, err := uc.CreateComment(context.Background(), "u1", "aid-draft", "hello", "", "")
	if !errors.Is(err, ErrArticleNotFound) {
		t.Fatalf("expected ErrArticleNotFound, got %v", err)
	}
	if code := kerrors.Code(err); code != 404 {
		t.Fatalf("expected HTTP 404, got %d", code)
	}
	if repo.created != nil {
		t.Fatal("comment should not have been created for unpublished article")
	}
}

func TestCreateCommentClearsForgedReplyname(t *testing.T) {
	for _, tc := range []struct {
		name      string
		replycid  string
		replyname string
	}{
		{name: "empty replycid", replycid: "", replyname: "forged-admin"},
		{name: "whitespace replycid", replycid: "   ", replyname: "forged-admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &stubCommentRepo{
				published: map[string]bool{"aid-1": true},
				users:     map[string]*User{"u1": {UID: "u1", Name: "Alice"}},
			}
			uc := NewCommentUsecase(repo, log.NewStdLogger(io.Discard))

			created, err := uc.CreateComment(context.Background(), "u1", "aid-1", "top level", tc.replycid, tc.replyname)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if created.Replycid != "" || created.Replyname != "" {
				t.Fatalf("expected empty replycid and replyname, got replycid=%q replyname=%q", created.Replycid, created.Replyname)
			}
		})
	}
}

func TestCreateCommentDomainErrors(t *testing.T) {
	t.Run("missing user returns 404 ErrUserNotFound", func(t *testing.T) {
		repo := &stubCommentRepo{
			published: map[string]bool{"aid-1": true},
			users:     map[string]*User{},
		}
		uc := NewCommentUsecase(repo, log.NewStdLogger(io.Discard))

		_, err := uc.CreateComment(context.Background(), "missing-u", "aid-1", "hello", "", "")
		if !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("expected ErrUserNotFound, got %v", err)
		}
		if code := kerrors.Code(err); code != 404 {
			t.Fatalf("expected HTTP 404, got %d", code)
		}
	})

	t.Run("downstream user service error returns 500 ErrInternalError", func(t *testing.T) {
		repo := &stubCommentRepo{
			published: map[string]bool{"aid-1": true},
			userErr:   errors.New("dial tcp: connection refused"),
		}
		uc := NewCommentUsecase(repo, log.NewStdLogger(io.Discard))

		_, err := uc.CreateComment(context.Background(), "u1", "aid-1", "hello", "", "")
		if !errors.Is(err, ErrInternalError) {
			t.Fatalf("expected ErrInternalError, got %v", err)
		}
		if code := kerrors.Code(err); code != 500 {
			t.Fatalf("expected HTTP 500, got %d", code)
		}
	})

	t.Run("missing parent comment returns 404 ErrParentCommentNotFound", func(t *testing.T) {
		repo := &stubCommentRepo{
			published: map[string]bool{"aid-1": true},
			users:     map[string]*User{"u1": {UID: "u1", Name: "Alice"}},
			comments:  map[string]*Comment{},
		}
		uc := NewCommentUsecase(repo, log.NewStdLogger(io.Discard))

		_, err := uc.CreateComment(context.Background(), "u1", "aid-1", "reply", "missing-cid", "fake")
		if !errors.Is(err, ErrParentCommentNotFound) {
			t.Fatalf("expected ErrParentCommentNotFound, got %v", err)
		}
		if code := kerrors.Code(err); code != 404 {
			t.Fatalf("expected HTTP 404, got %d", code)
		}
	})

	t.Run("deleted parent comment returns 404 ErrParentCommentNotFound", func(t *testing.T) {
		repo := &stubCommentRepo{
			published: map[string]bool{"aid-1": true},
			users:     map[string]*User{"u1": {UID: "u1", Name: "Alice"}},
			comments: map[string]*Comment{
				"c-del": {CID: "c-del", Target: "aid-1", Status: "deleted", Name: "Bob"},
			},
		}
		uc := NewCommentUsecase(repo, log.NewStdLogger(io.Discard))

		_, err := uc.CreateComment(context.Background(), "u1", "aid-1", "reply", "c-del", "")
		if !errors.Is(err, ErrParentCommentNotFound) {
			t.Fatalf("expected ErrParentCommentNotFound, got %v", err)
		}
		if code := kerrors.Code(err); code != 404 {
			t.Fatalf("expected HTTP 404, got %d", code)
		}
	})
}

func TestGetCommentListUnpublishedArticleReturnsEmpty(t *testing.T) {
	repo := &stubCommentRepo{
		published:  map[string]bool{"aid-pub": true},
		listResult: []*Comment{{CID: "c1", Content: "secret", Status: "active"}},
		listTotal:  1,
	}
	uc := NewCommentUsecase(repo, log.NewStdLogger(io.Discard))

	comments, total, err := uc.GetCommentList(context.Background(), "smoke-none", 1, 20)
	if err != nil {
		t.Fatalf("expected nil error for unpublished article, got %v", err)
	}
	if total != 0 || len(comments) != 0 {
		t.Fatalf("expected empty comments and total=0, got len=%d total=%d", len(comments), total)
	}
	if repo.listCalled {
		t.Fatal("repo.GetCommentList should not be called for unpublished article")
	}

	withReplies, totalWithReplies, err := uc.GetCommentListWithReplies(context.Background(), "smoke-none", 1, 20)
	if err != nil {
		t.Fatalf("expected nil error for unpublished article with replies, got %v", err)
	}
	if totalWithReplies != 0 || len(withReplies) != 0 {
		t.Fatalf("expected empty comments and total=0, got len=%d total=%d", len(withReplies), totalWithReplies)
	}
	if repo.listCalled {
		t.Fatal("repo.GetCommentListWithReplies should not be called for unpublished article")
	}
}
