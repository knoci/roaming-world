package data

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/knoci/roaming-world/user/internal/biz"
	"gorm.io/gorm/clause"
)

type Follow struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FollowerUID string    `gorm:"type:varchar(36);not null;uniqueIndex:uk_follow_pair,priority:1" json:"follower_uid"`
	FolloweeUID string    `gorm:"type:varchar(36);not null;uniqueIndex:uk_follow_pair,priority:2;index:idx_follows_followee" json:"followee_uid"`
	CreatedAt   time.Time `json:"created_at"`
}

type Notification struct {
	ID        string     `gorm:"primaryKey;type:varchar(36)" json:"id"`
	UID       string     `gorm:"type:varchar(36);not null;index:idx_notifications_uid_read,priority:1" json:"uid"`
	Type      string     `gorm:"type:varchar(30);not null" json:"type"`
	ActorUID  string     `gorm:"type:varchar(36)" json:"actor_uid"`
	ActorName string     `gorm:"type:varchar(50)" json:"actor_name"`
	Payload   string     `gorm:"type:text" json:"payload"`
	ReadAt    *time.Time `gorm:"index:idx_notifications_uid_read,priority:2" json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type socialRepo struct {
	data *Data
	log  *log.Helper
}

func NewSocialRepo(data *Data, logger log.Logger) biz.SocialRepo {
	return &socialRepo{data: data, log: log.NewHelper(logger)}
}

func (r *socialRepo) Follow(ctx context.Context, followerUID, followeeUID string) (bool, error) {
	row := &Follow{FollowerUID: followerUID, FolloweeUID: followeeUID}
	res := r.data.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *socialRepo) Unfollow(ctx context.Context, followerUID, followeeUID string) error {
	return r.data.db.WithContext(ctx).Where("follower_uid = ? AND followee_uid = ?", followerUID, followeeUID).
		Delete(&Follow{}).Error
}

func (r *socialRepo) Followers(ctx context.Context, uid string, page, limit int) ([]*biz.UserSummary, int64, error) {
	base := r.data.db.WithContext(ctx).Table("follows").Where("followee_uid = ?", uid)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []struct {
		UID    string
		Name   string
		Avatar string
	}
	if err := r.data.db.WithContext(ctx).Table("follows").
		Select("users.uid, users.name, users.avatar").
		Joins("JOIN users ON users.uid = follows.follower_uid").
		Where("follows.followee_uid = ?", uid).
		Order("follows.created_at DESC").
		Offset((page - 1) * limit).Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.UserSummary, 0, len(rows))
	for _, row := range rows {
		result = append(result, &biz.UserSummary{UID: row.UID, Name: row.Name, Avatar: row.Avatar})
	}
	return result, total, nil
}

func (r *socialRepo) Following(ctx context.Context, uid string, page, limit int) ([]*biz.UserSummary, int64, error) {
	base := r.data.db.WithContext(ctx).Table("follows").Where("follower_uid = ?", uid)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []struct {
		UID    string
		Name   string
		Avatar string
	}
	if err := r.data.db.WithContext(ctx).Table("follows").
		Select("users.uid, users.name, users.avatar").
		Joins("JOIN users ON users.uid = follows.followee_uid").
		Where("follows.follower_uid = ?", uid).
		Order("follows.created_at DESC").
		Offset((page - 1) * limit).Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.UserSummary, 0, len(rows))
	for _, row := range rows {
		result = append(result, &biz.UserSummary{UID: row.UID, Name: row.Name, Avatar: row.Avatar})
	}
	return result, total, nil
}

func (r *socialRepo) FollowingUIDs(ctx context.Context, uid string) ([]string, error) {
	var uids []string
	if err := r.data.db.WithContext(ctx).Model(&Follow{}).
		Where("follower_uid = ?", uid).Pluck("followee_uid", &uids).Error; err != nil {
		return nil, err
	}
	return uids, nil
}

func (r *socialRepo) CreateNotification(ctx context.Context, n *biz.Notification) error {
	row := &Notification{
		ID: uuid.New().String(), UID: n.UID, Type: n.Type,
		ActorUID: n.ActorUID, ActorName: n.ActorName, Payload: n.Payload,
	}
	return r.data.db.WithContext(ctx).Create(row).Error
}

func (r *socialRepo) ListNotifications(ctx context.Context, uid string, page, limit int) ([]*biz.Notification, int64, int64, error) {
	q := r.data.db.WithContext(ctx).Model(&Notification{}).Where("uid = ?", uid)
	var total, unread int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, 0, err
	}
	if err := r.data.db.WithContext(ctx).Model(&Notification{}).Where("uid = ? AND read_at IS NULL", uid).Count(&unread).Error; err != nil {
		return nil, 0, 0, err
	}
	var rows []Notification
	if err := q.Order("created_at DESC").Offset((page - 1) * limit).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, 0, err
	}
	result := make([]*biz.Notification, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		result = append(result, &biz.Notification{
			ID: row.ID, Type: row.Type, ActorUID: row.ActorUID, ActorName: row.ActorName,
			Payload: row.Payload, Read: row.ReadAt != nil, CreatedAt: row.CreatedAt,
		})
	}
	return result, total, unread, nil
}

func (r *socialRepo) MarkNotificationRead(ctx context.Context, uid, id string) error {
	now := time.Now()
	res := r.data.db.WithContext(ctx).Model(&Notification{}).
		Where("id = ? AND uid = ? AND read_at IS NULL", id, uid).
		Update("read_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var count int64
		r.data.db.WithContext(ctx).Model(&Notification{}).Where("id = ? AND uid = ?", id, uid).Count(&count)
		if count == 0 {
			return biz.ErrNotificationNotFound
		}
	}
	return nil
}
