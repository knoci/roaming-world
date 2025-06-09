package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/knoci/roaming-world/comment/internal/biz"
	"gorm.io/gorm"
)

// Comment 评论模型
type Comment struct {
	CID       string    `gorm:"primaryKey;type:varchar(36);column:cid" json:"cid"`
	Target    string    `gorm:"type:varchar(36)" json:"target"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	Likes     int       `gorm:"default:0" json:"likes"`
	UID       string    `gorm:"type:varchar(36);column:uid" json:"uid"`
	Name      string    `gorm:"type:varchar(50);not null" json:"name"`
	Avatar    string    `gorm:"type:varchar(100)" json:"avatar"`
	Replycid  string    `gorm:"type:varchar(36)" json:"replycid"`
	Replyname string    `gorm:"type:varchar(36)" json:"replyname"`
	Time      string    `gorm:"type:varchar(36)" json:"time"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (c *Comment) BeforeCreate(tx *gorm.DB) error {
	if c.CID == "" {
		c.CID = uuid.New().String()
	}
	return nil
}

// Commentlike 评论点赞模型
type Commentlike struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UID       string    `gorm:"type:varchar(36);column:uid" json:"uid"`
	CID       string    `gorm:"type:varchar(36);column:cid" json:"cid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type commentRepo struct {
	data *Data
	log  *log.Helper
}

// NewCommentRepo 创建评论仓库实例
func NewCommentRepo(data *Data, logger log.Logger) biz.CommentRepo {
	return &commentRepo{
		data: data,
		log:  log.NewHelper(logger),
	}
}

// Create 创建评论
func (r *commentRepo) Create(ctx context.Context, comment *biz.Comment) (*biz.Comment, error) {
	c := &Comment{
		Target:    comment.Target,
		Content:   comment.Content,
		UID:       comment.UID,
		Name:      comment.Name,
		Avatar:    comment.Avatar,
		Replycid:  comment.Replycid,
		Replyname: comment.Replyname,
		Time:      comment.Time,
		Likes:     comment.Likes,
	}

	result := r.data.db.Create(c)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("commentRepo: create comment error: %v", result.Error)
		error := r.data.SendErrorLog(ctx, "comment", result.Error.Error(), "db.Create", c)
		if error != nil {
			r.log.WithContext(ctx).Errorf("commentRepo: kafka send errorlog error: %v", error)
		}
		return nil, result.Error
	}

	sql := `INSERT INTO comments (cid, target, content, uid, name, avatar, replycid, replyname, time, likes) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	params := []any{c.CID, c.Target, c.Content, c.UID, c.Name, c.Avatar, c.Replycid, c.Replyname, c.Time, c.Likes}
	error := r.data.SendSqlLog(ctx, "comment", sql, params)
	if error != nil {
		r.log.WithContext(ctx).Errorf("commentRepo: kafka send sqllog error: %v", error)
	}

	return &biz.Comment{
		CID:       c.CID,
		Target:    c.Target,
		Content:   c.Content,
		Likes:     c.Likes,
		UID:       c.UID,
		Name:      c.Name,
		Avatar:    c.Avatar,
		Replycid:  c.Replycid,
		Replyname: c.Replyname,
		Time:      c.Time,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}, nil
}

// Delete 删除评论
func (r *commentRepo) Delete(ctx context.Context, uid, cid string) error {
	// 检查评论是否存在
	var comment Comment
	if err := r.data.db.Where("cid = ?", cid).First(&comment).Error; err != nil {
		return biz.ErrCommentNotFound
	}

	// 检查是否有权限删除评论
	if comment.UID != uid {
		return biz.ErrNoPermission
	}

	// 开启事务
	tx := r.data.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 删除评论的点赞记录
	if err := tx.Where("cid = ?", cid).Delete(&Commentlike{}).Error; err != nil {
		tx.Rollback()
		return err
	}
	// 发送SQL日志到Kafka
	sql := `DELETE FROM commentlikes WHERE cid = $1`
	params := []any{cid}
	if err := r.data.SendSqlLog(ctx, "comment", sql, params); err != nil {
		r.log.WithContext(ctx).Errorf("commentRepo: kafka send sqllog error: %v", err)
	}

	// 如果是一级评论，删除其所有二级评论及其点赞记录
	if comment.Replycid == "" {
		// 获取所有二级评论
		var replies []Comment
		if err := tx.Where("replycid = ?", cid).Find(&replies).Error; err != nil {
			tx.Rollback()
			return err
		}

		// 删除所有二级评论的点赞记录
		for _, reply := range replies {
			if err := tx.Where("cid = ?", reply.CID).Delete(&Commentlike{}).Error; err != nil {
				tx.Rollback()
				return err
			}
			// 发送SQL日志到Kafka
			sql := `DELETE FROM commentlikes WHERE cid = $1`
			params := []any{reply.CID}
			if err := r.data.SendSqlLog(ctx, "comment", sql, params); err != nil {
				r.log.WithContext(ctx).Errorf("commentRepo: kafka send sqllog error: %v", err)
			}
		}

		// 删除所有二级评论
		if err := tx.Where("replycid = ?", cid).Delete(&Comment{}).Error; err != nil {
			tx.Rollback()
			return err
		}
		// 发送SQL日志到Kafka
		sql = `DELETE FROM comments WHERE replycid = $1`
		params = []any{cid}
		if err := r.data.SendSqlLog(ctx, "comment", sql, params); err != nil {
			r.log.WithContext(ctx).Errorf("commentRepo: kafka send sqllog error: %v", err)
		}

		// 更新文章评论数
		commentCount := len(replies) + 1 // 二级评论数量加上一级评论本身
		if err := r.UpdateArticleCommentCount(ctx, comment.Target, -commentCount); err != nil {
			tx.Rollback()
			return err
		}

		// 更新Redis中的评论数
		if err := r.UpdateRedisArticleCommentCount(ctx, comment.Target, float64(-commentCount)); err != nil {
			r.log.WithContext(ctx).Errorf("commentRepo: update Redis article comments count error: %v", err)
		}
	}

	// 删除评论本身
	if err := tx.Delete(&comment).Error; err != nil {
		tx.Rollback()
		return err
	}
	// 发送SQL日志到Kafka
	sql = `DELETE FROM comments WHERE cid = $1`
	params = []any{cid}
	if err := r.data.SendSqlLog(ctx, "comment", sql, params); err != nil {
		r.log.WithContext(ctx).Errorf("commentRepo: kafka send sqllog error: %v", err)
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		return err
	}

	return nil
}

// Like 点赞评论
func (r *commentRepo) Like(ctx context.Context, uid, cid string) error {
	// 检查评论是否存在
	var comment Comment
	if err := r.data.db.WithContext(ctx).Where("cid = ?", cid).First(&comment).Error; err != nil {
		return biz.ErrCommentNotFound
	}

	// 检查是否已点赞
	var like Commentlike
	result := r.data.db.Where("uid = ? AND cid = ?", uid, cid).First(&like)

	// 开启事务
	return r.data.db.Transaction(func(tx *gorm.DB) error {
		if result.Error == gorm.ErrRecordNotFound {
			// 创建点赞记录
			if err := tx.Create(&Commentlike{UID: uid, CID: cid}).Error; err != nil {
				return err
			}
			// 发送SQL日志到Kafka
			sql := `INSERT INTO commentlikes (uid, cid) VALUES ($1, $2)`
			params := []any{uid, cid}
			if err := r.data.SendSqlLog(ctx, "comment", sql, params); err != nil {
				r.log.WithContext(ctx).Errorf("commentRepo: kafka send sqllog error: %v", err)
			}
			// 更新点赞数
			if err := tx.Model(&comment).UpdateColumn("likes", gorm.Expr("likes + ?", 1)).Error; err != nil {
				return err
			}
			// 发送SQL日志到Kafka
			sql = `UPDATE comments SET likes = likes + 1 WHERE cid = $1`
			params = []any{cid}
			if err := r.data.SendSqlLog(ctx, "comment", sql, params); err != nil {
				r.log.WithContext(ctx).Errorf("commentRepo: kafka send sqllog error: %v", err)
			}
		} else {
			// 删除点赞记录
			if err := tx.Delete(&like).Error; err != nil {
				return err
			}
			// 发送SQL日志到Kafka
			sql := `DELETE FROM commentlikes WHERE uid = $1 AND cid = $2`
			params := []any{uid, cid}
			if err := r.data.SendSqlLog(ctx, "comment", sql, params); err != nil {
				r.log.WithContext(ctx).Errorf("commentRepo: kafka send sqllog error: %v", err)
			}
			// 更新点赞数
			if err := tx.Model(&comment).UpdateColumn("likes", gorm.Expr("likes - ?", 1)).Error; err != nil {
				return err
			}
			// 发送SQL日志到Kafka
			sql = `UPDATE comments SET likes = likes - 1 WHERE cid = $1`
			params = []any{cid}
			if err := r.data.SendSqlLog(ctx, "comment", sql, params); err != nil {
				r.log.WithContext(ctx).Errorf("commentRepo: kafka send sqllog error: %v", err)
			}
		}
		return nil
	})
}

// GetCommentList 获取一级评论列表
func (r *commentRepo) GetCommentList(ctx context.Context, aid string) ([]*biz.Comment, error) {
	var comments []Comment
	if err := r.data.db.WithContext(ctx).Where("target = ? AND replycid = ''", aid).Order("created_at desc").Find(&comments).Error; err != nil {
		return nil, err
	}

	result := make([]*biz.Comment, 0, len(comments))
	for _, c := range comments {
		result = append(result, &biz.Comment{
			CID:       c.CID,
			Target:    c.Target,
			Content:   c.Content,
			Likes:     c.Likes,
			UID:       c.UID,
			Name:      c.Name,
			Avatar:    c.Avatar,
			Replycid:  c.Replycid,
			Replyname: c.Replyname,
			Time:      c.Time,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}

	return result, nil
}

// GetReplyList 获取二级评论列表
func (r *commentRepo) GetReplyList(ctx context.Context, cid string) ([]*biz.Comment, error) {
	var comments []Comment
	if err := r.data.db.WithContext(ctx).Where("replycid = ?", cid).Order("created_at desc").Find(&comments).Error; err != nil {
		return nil, err
	}

	result := make([]*biz.Comment, 0, len(comments))
	for _, c := range comments {
		result = append(result, &biz.Comment{
			CID:       c.CID,
			Target:    c.Target,
			Content:   c.Content,
			Likes:     c.Likes,
			UID:       c.UID,
			Name:      c.Name,
			Avatar:    c.Avatar,
			Replycid:  c.Replycid,
			Replyname: c.Replyname,
			Time:      c.Time,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}

	return result, nil
}

// GetCommentListWithReplies 获取一级评论列表及其前两条二级评论
func (r *commentRepo) GetCommentListWithReplies(ctx context.Context, aid string) ([]*biz.Comment, error) {
	// 开启事务
	tx := r.data.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取一级评论列表
	var comments []Comment
	if err := tx.Where("target = ? AND replycid = ''", aid).Order("created_at desc").Find(&comments).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	result := make([]*biz.Comment, 0, len(comments))
	for _, c := range comments {
		result = append(result, &biz.Comment{
			CID:       c.CID,
			Target:    c.Target,
			Content:   c.Content,
			Likes:     c.Likes,
			UID:       c.UID,
			Name:      c.Name,
			Avatar:    c.Avatar,
			Replycid:  c.Replycid,
			Replyname: c.Replyname,
			Time:      c.Time,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}

	// 获取每个一级评论的前两条二级评论并追加到result切片中
	for _, comment := range comments {
		var replies []Comment
		if err := tx.Where("replycid = ?", comment.CID).Order("created_at desc").Limit(2).Find(&replies).Error; err != nil {
			r.log.WithContext(ctx).Errorf("commentRepo: failed to get reply comments: %v", err)
			continue
		}

		// 将二级评论追加到result切片中
		for _, reply := range replies {
			result = append(result, &biz.Comment{
				CID:       reply.CID,
				Target:    reply.Target,
				Content:   reply.Content,
				Likes:     reply.Likes,
				UID:       reply.UID,
				Name:      reply.Name,
				Avatar:    reply.Avatar,
				Replycid:  reply.Replycid,
				Replyname: reply.Replyname,
				Time:      reply.Time,
				CreatedAt: reply.CreatedAt,
				UpdatedAt: reply.UpdatedAt,
			})
		}
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	return result, nil
}

// FindUser 查找用户
func (r *commentRepo) FindUser(ctx context.Context, uid string) (*biz.User, error) {
	// 这里应该调用user服务的gRPC接口获取用户信息
	type User struct {
		UID    string `gorm:"primaryKey;type:varchar(36);column:uid"`
		Name   string `gorm:"type:varchar(50);not null"`
		Avatar string `gorm:"type:varchar(100)"`
		Email  string `gorm:"type:varchar(20);not null;unique"`
	}

	var user User
	result := r.data.db.WithContext(ctx).Table("users").Where("uid = ?", uid).First(&user)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, biz.ErrUserNotFound
		}
		return nil, result.Error
	}

	return &biz.User{
		UID:    user.UID,
		Name:   user.Name,
		Avatar: user.Avatar,
		Email:  user.Email,
	}, nil
}

// UpdateArticleCommentCount 更新文章评论数
func (r *commentRepo) UpdateArticleCommentCount(ctx context.Context, aid string, count int) error {
	type Article struct {
		AID      string `gorm:"primaryKey;type:varchar(36);column:aid"`
		Comments int    `gorm:"default:0"`
	}

	result := r.data.db.WithContext(ctx).Table("articles").Where("aid = ?", aid).UpdateColumn("comments", gorm.Expr("comments + ?", count))
	if result.Error != nil {
		return result.Error
	}

	// 发送SQL日志到Kafka
	sql := fmt.Sprintf("UPDATE articles SET comments = comments + %d WHERE aid = $1", count)
	params := []any{aid}
	if err := r.data.SendSqlLog(ctx, "comment", sql, params); err != nil {
		r.log.WithContext(ctx).Errorf("commentRepo: kafka send sqllog error: %v", err)
	}

	return nil
}

// UpdateRedisArticleCommentCount 更新Redis中的文章评论数
func (r *commentRepo) UpdateRedisArticleCommentCount(ctx context.Context, aid string, count float64) error {
	err := r.data.redis.ZIncrBy(ctx, "article_comments", count, aid).Err()
	if err != nil {
		return err
	}

	return nil
}
