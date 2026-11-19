package service

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	pb "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/biz"
	"google.golang.org/grpc/metadata"
)

type internalRPCRepo struct {
	biz.SocialRepo
	notification *biz.Notification
}

func (r *internalRPCRepo) FollowingUIDs(_ context.Context, _ string) ([]string, error) {
	return []string{"followed-author"}, nil
}

func (r *internalRPCRepo) CreateNotification(_ context.Context, notification *biz.Notification) error {
	r.notification = notification
	return nil
}

func TestInternalRPCRequiresTheCallingServiceToken(t *testing.T) {
	const articleToken = "fixture-article-token"
	const commentToken = "fixture-comment-token"
	t.Setenv("ARTICLE_USER_RPC_TOKEN", articleToken)
	t.Setenv("COMMENT_USER_RPC_TOKEN", commentToken)
	repo := &internalRPCRepo{}
	server := &UserService{social: biz.NewSocialUsecase(repo, nil, log.NewStdLogger(io.Discard))}
	withTokens := func(tokens ...string) context.Context {
		return metadata.NewIncomingContext(context.Background(), metadata.MD{"x-roaming-service-token": tokens})
	}
	follow := &pb.GetFollowingUIDsRequest{Uid: "reader"}
	notify := &pb.CreateNotificationRequest{Uid: "reader", ActorUid: "author", Type: "comment_reply"}

	for _, ctx := range []context.Context{context.Background(), withTokens(commentToken), withTokens(articleToken, articleToken)} {
		if _, err := server.GetFollowingUIDs(ctx, follow); !errors.Is(err, biz.ErrUnauthorized) {
			t.Fatalf("follow list accepted unauthorized credentials: %v", err)
		}
	}
	for _, ctx := range []context.Context{context.Background(), withTokens(articleToken), withTokens(commentToken, commentToken)} {
		if _, err := server.CreateNotification(ctx, notify); !errors.Is(err, biz.ErrUnauthorized) {
			t.Fatalf("notification accepted unauthorized credentials: %v", err)
		}
	}
	if repo.notification != nil {
		t.Fatal("unauthorized request reached the repository")
	}
	followed, err := server.GetFollowingUIDs(withTokens(articleToken), follow)
	if err != nil || len(followed.GetUids()) != 1 || followed.GetUids()[0] != "followed-author" {
		t.Fatalf("authorized follow list returned %v, %v", followed, err)
	}
	if _, err := server.CreateNotification(withTokens(commentToken), notify); err != nil || repo.notification == nil {
		t.Fatalf("authorized notification failed: %v", err)
	}
	notify.Type = "follow"
	if _, err := server.CreateNotification(withTokens(commentToken), notify); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("comment service could create a different notification type: %v", err)
	}
	t.Setenv("ARTICLE_USER_RPC_TOKEN", "")
	if _, err := server.GetFollowingUIDs(withTokens(""), follow); !errors.Is(err, biz.ErrUnauthorized) {
		t.Fatalf("unconfigured RPC accepted an empty token: %v", err)
	}
}
