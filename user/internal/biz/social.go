package biz

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

type UserSummary struct {
	UID    string
	Name   string
	Avatar string
}

type Notification struct {
	ID        string
	UID       string
	Type      string
	ActorUID  string
	ActorName string
	Payload   string
	Read      bool
	CreatedAt time.Time
}

type SocialRepo interface {
	Follow(ctx context.Context, followerUID, followeeUID string) (bool, error)
	Unfollow(ctx context.Context, followerUID, followeeUID string) error
	Followers(ctx context.Context, uid string, page, limit int) ([]*UserSummary, int64, error)
	Following(ctx context.Context, uid string, page, limit int) ([]*UserSummary, int64, error)
	FollowingUIDs(ctx context.Context, uid string) ([]string, error)
	CreateNotification(ctx context.Context, n *Notification) error
	ListNotifications(ctx context.Context, uid string, page, limit int) ([]*Notification, int64, int64, error)
	MarkNotificationRead(ctx context.Context, uid, id string) error
}

type SocialUsecase struct {
	repo  SocialRepo
	users UserRepo
	log   *log.Helper
}

func NewSocialUsecase(repo SocialRepo, users UserRepo, logger log.Logger) *SocialUsecase {
	return &SocialUsecase{repo: repo, users: users, log: log.NewHelper(logger)}
}

func (uc *SocialUsecase) CreateNotification(ctx context.Context, n *Notification) error {
	if n.UID == "" || n.ActorUID == "" {
		return ErrInvalidArgument
	}
	switch n.Type {
	case "follow", "comment_reply":
	default:
		return ErrInvalidArgument
	}
	if n.ActorUID == n.UID {
		return nil
	}
	return uc.repo.CreateNotification(ctx, n)
}

func (uc *SocialUsecase) Follow(ctx context.Context, followerUID, followeeUID string) (bool, error) {
	if followerUID == "" || followeeUID == "" {
		return false, ErrUnauthorized
	}
	if followerUID == followeeUID {
		return false, ErrCannotFollowSelf
	}
	target, err := uc.users.FindByUID(ctx, followeeUID)
	if err != nil || target.Status == "banned" {
		return false, ErrUserNotFound
	}
	actor, err := uc.users.FindByUID(ctx, followerUID)
	if err != nil {
		return false, ErrUserNotFound
	}
	created, err := uc.repo.Follow(ctx, followerUID, followeeUID)
	if err != nil {
		return false, ErrInternalError
	}
	if created {
		if err := uc.repo.CreateNotification(ctx, &Notification{
			UID: followeeUID, Type: "follow", ActorUID: actor.UID,
			ActorName: actor.Name, Payload: actor.UID,
		}); err != nil {
			uc.log.WithContext(ctx).Errorf("socialUsecase: create follow notification failed: %v", err)
		}
	}
	return true, nil
}

func (uc *SocialUsecase) Unfollow(ctx context.Context, followerUID, followeeUID string) error {
	if followerUID == "" || followeeUID == "" {
		return ErrUnauthorized
	}
	return uc.repo.Unfollow(ctx, followerUID, followeeUID)
}

func (uc *SocialUsecase) Followers(ctx context.Context, uid string, page, limit int) ([]*UserSummary, int64, error) {
	page, limit = normalizeSocialPage(page, limit)
	return uc.repo.Followers(ctx, uid, page, limit)
}

func (uc *SocialUsecase) Following(ctx context.Context, uid string, page, limit int) ([]*UserSummary, int64, error) {
	page, limit = normalizeSocialPage(page, limit)
	return uc.repo.Following(ctx, uid, page, limit)
}

func (uc *SocialUsecase) FollowingUIDs(ctx context.Context, uid string) ([]string, error) {
	if uid == "" {
		return nil, ErrUnauthorized
	}
	return uc.repo.FollowingUIDs(ctx, uid)
}

func (uc *SocialUsecase) Notifications(ctx context.Context, uid string, page, limit int) ([]*Notification, int64, int64, error) {
	if uid == "" {
		return nil, 0, 0, ErrUnauthorized
	}
	page, limit = normalizeSocialPage(page, limit)
	return uc.repo.ListNotifications(ctx, uid, page, limit)
}

func (uc *SocialUsecase) MarkNotificationRead(ctx context.Context, uid, id string) error {
	return uc.repo.MarkNotificationRead(ctx, uid, id)
}

func normalizeSocialPage(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	return page, limit
}
