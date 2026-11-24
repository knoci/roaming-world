package data

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	pd "github.com/knoci/roaming-world/comment/api/user/v1"
	"github.com/knoci/roaming-world/comment/internal/biz"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeUserClient struct {
	pd.UserClient
	resp *pd.FindUserReply
	err  error
}

func (f *fakeUserClient) FindUser(context.Context, *pd.FindUserRequest, ...grpc.CallOption) (*pd.FindUserReply, error) {
	return f.resp, f.err
}

func TestToBizCommentMasksDeletedContent(t *testing.T) {
	got := toBizComment(&Comment{CID: "c1", Content: "secret original", Status: "deleted"})
	if got.Content != deletedCommentContent {
		t.Fatalf("expected masked content %q, got %q", deletedCommentContent, got.Content)
	}

	active := toBizComment(&Comment{CID: "c2", Content: "hello", Status: "active"})
	if active.Content != "hello" {
		t.Fatalf("expected active content %q, got %q", "hello", active.Content)
	}
}

func TestFindUserDistinguishesNotFoundAndInternalError(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)

	t.Run("grpc NotFound maps to ErrUserNotFound", func(t *testing.T) {
		repo := NewCommentRepo(&Data{userClient: &fakeUserClient{err: status.Error(codes.NotFound, "not found")}}, logger)
		_, err := repo.FindUser(context.Background(), "u-missing")
		if !errors.Is(err, biz.ErrUserNotFound) {
			t.Fatalf("expected ErrUserNotFound, got %v", err)
		}
	})

	t.Run("grpc Unavailable maps to ErrInternalError", func(t *testing.T) {
		repo := NewCommentRepo(&Data{userClient: &fakeUserClient{err: status.Error(codes.Unavailable, "user svc down")}}, logger)
		_, err := repo.FindUser(context.Background(), "u1")
		if !errors.Is(err, biz.ErrInternalError) {
			t.Fatalf("expected ErrInternalError, got %v", err)
		}
	})

	t.Run("valid user returned", func(t *testing.T) {
		repo := NewCommentRepo(&Data{userClient: &fakeUserClient{resp: &pd.FindUserReply{Uid: "u1", Name: "Alice"}}}, logger)
		u, err := repo.FindUser(context.Background(), "u1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.UID != "u1" || u.Name != "Alice" {
			t.Fatalf("unexpected user: %+v", u)
		}
	})
}
