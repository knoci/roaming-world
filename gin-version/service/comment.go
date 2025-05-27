package service

import (
	"context"
	"errors"
	"time"
	"travel-world/initialize"
	"travel-world/model"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type CommentService struct {
}

func NewCommentService() *CommentService {
	return &CommentService{}
}

// CreateComment 创建评论
func (s *CommentService) CreateComment(uid, target, content, replycid, replyname string) (*model.Comment, error) {
	// 获取用户信息
	var user model.User
	if err := initialize.DB.Where("uid = ?", uid).First(&user).Error; err != nil {
		return nil, errors.New("用户不存在")
	}

	// 如果有replycid，检查是否为二级评论
	if replycid != "" {
		var parentComment model.Comment
		if err := initialize.DB.Where("cid = ?", replycid).First(&parentComment).Error; err != nil {
			return nil, errors.New("回复的评论不存在")
		}
		// 如果父评论是二级评论，使用其replycid
		if parentComment.Replycid != "" {
			replycid = parentComment.Replycid
		}
	}

	// 创建评论
	comment := &model.Comment{
		Target:    target,
		Content:   content,
		UID:       uid,
		Name:      user.Name,
		Avatar:    user.Avatar,
		Replycid:  replycid,
		Replyname: replyname,
		Time:      time.Now().Format("2006-01-02 15:04:05"),
		Likes:     0,
	}

	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 保存评论
	if err := tx.Create(comment).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 更新文章评论数
	if replycid == "" {
		// 更新数据库中的评论数
		if err := tx.Model(&model.Article{}).Where("aid = ?", target).UpdateColumn("comments", gorm.Expr("comments + ?", 1)).Error; err != nil {
			tx.Rollback()
			return nil, err
		}
		// 更新Redis中的评论数
		ctx := context.Background()
		if err := initialize.RDB.ZIncrBy(ctx, "article_comments", 1, target).Err(); err != nil {
			initialize.Logger.Error("Failed to update Redis article comments count", zap.Error(err))
		}
		// 发送SQL日志到Kafka
		if err := initialize.SendSQLLog("UPDATE articles SET comments = comments + 1 WHERE aid = '%s'", target); err != nil {
			initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
		}
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("INSERT INTO comments (cid, target, content, uid, name, avatar, replycid, replyname, time, likes) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', %d)",
		comment.CID, comment.Target, comment.Content, comment.UID, comment.Name, comment.Avatar, comment.Replycid, comment.Replyname, comment.Time, comment.Likes); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	return comment, nil
}

// LikeComment 点赞评论
func (s *CommentService) LikeComment(uid, cid string) error {
	// 检查评论是否存在
	var comment model.Comment
	if err := initialize.DB.Where("cid = ?", cid).First(&comment).Error; err != nil {
		return errors.New("评论不存在")
	}

	// 检查是否已点赞
	var like model.Commentlike
	result := initialize.DB.Where("uid = ? AND cid = ?", uid, cid).First(&like)

	// 开启事务
	return initialize.DB.Transaction(func(tx *gorm.DB) error {
		if result.Error == gorm.ErrRecordNotFound {
			// 创建点赞记录
			if err := tx.Create(&model.Commentlike{UID: uid, CID: cid}).Error; err != nil {
				return err
			}
			// 发送SQL日志到Kafka
			if err := initialize.SendSQLLog("INSERT INTO commentlikes (uid, cid) VALUES ('%s', '%s')", uid, cid); err != nil {
				initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
			}
			// 更新点赞数
			if err := tx.Model(&comment).UpdateColumn("likes", gorm.Expr("likes + ?", 1)).Error; err != nil {
				return err
			}
			// 发送SQL日志到Kafka
			if err := initialize.SendSQLLog("UPDATE comments SET likes = likes + 1 WHERE cid = '%s'", cid); err != nil {
				initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
			}
		} else {
			// 删除点赞记录
			if err := tx.Delete(&like).Error; err != nil {
				return err
			}
			// 发送SQL日志到Kafka
			if err := initialize.SendSQLLog("DELETE FROM commentlikes WHERE uid = '%s' AND cid = '%s'", uid, cid); err != nil {
				initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
			}
			// 更新点赞数
			if err := tx.Model(&comment).UpdateColumn("likes", gorm.Expr("likes - ?", 1)).Error; err != nil {
				return err
			}
			// 发送SQL日志到Kafka
			if err := initialize.SendSQLLog("UPDATE comments SET likes = likes - 1 WHERE cid = '%s'", cid); err != nil {
				initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
			}
		}
		return nil
	})
}

// GetCommentList 获取一级评论列表
func (s *CommentService) GetCommentList(aid string) ([]model.Comment, error) {
	var comments []model.Comment
	if err := initialize.DB.Where("target = ? AND replycid = ''", aid).Order("created_at desc").Find(&comments).Error; err != nil {
		return nil, err
	}
	return comments, nil
}

// GetReplyList 获取二级评论列表
func (s *CommentService) GetReplyList(cid string) ([]model.Comment, error) {
	var comments []model.Comment
	if err := initialize.DB.Where("replycid = ?", cid).Order("created_at desc").Find(&comments).Error; err != nil {
		return nil, err
	}
	return comments, nil
}

// GetCommentListWithReplies 获取一级评论列表及其前两条二级评论
func (s *CommentService) GetCommentListWithReplies(aid string) ([]model.Comment, error) {
	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取一级评论列表
	var comments []model.Comment
	if err := tx.Where("target = ? AND replycid = ''", aid).Order("created_at desc").Find(&comments).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 获取每个一级评论的前两条二级评论并追加到comments切片中
	for i := range comments {
		var replies []model.Comment
		if err := tx.Where("replycid = ?", comments[i].CID).Order("created_at desc").Limit(2).Find(&replies).Error; err != nil {
			initialize.Logger.Error("Failed to get reply comments", zap.Error(err))
			continue
		}
		// 将二级评论追加到comments切片中
		comments = append(comments, replies...)
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	return comments, nil
}

// DeleteComment 删除评论
func (s *CommentService) DeleteComment(uid, cid string) error {
	// 检查评论是否存在
	var comment model.Comment
	if err := initialize.DB.Where("cid = ?", cid).First(&comment).Error; err != nil {
		return errors.New("评论不存在")
	}

	// 检查是否有权限删除评论
	if comment.UID != uid {
		return errors.New("无权限删除该评论")
	}

	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 删除评论的点赞记录
	if err := tx.Where("cid = ?", cid).Delete(&model.Commentlike{}).Error; err != nil {
		tx.Rollback()
		return err
	}
	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("DELETE FROM commentlikes WHERE cid = '%s'", cid); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 如果是一级评论，删除其所有二级评论及其点赞记录
	if comment.Replycid == "" {
		// 获取所有二级评论
		var replies []model.Comment
		if err := tx.Where("replycid = ?", cid).Find(&replies).Error; err != nil {
			tx.Rollback()
			return err
		}

		// 删除所有二级评论的点赞记录
		for _, reply := range replies {
			if err := tx.Where("cid = ?", reply.CID).Delete(&model.Commentlike{}).Error; err != nil {
				tx.Rollback()
				return err
			}
			// 发送SQL日志到Kafka
			if err := initialize.SendSQLLog("DELETE FROM commentlikes WHERE cid = '%s'", reply.CID); err != nil {
				initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
			}
		}

		// 删除所有二级评论
		if err := tx.Where("replycid = ?", cid).Delete(&model.Comment{}).Error; err != nil {
			tx.Rollback()
			return err
		}
		// 发送SQL日志到Kafka
		if err := initialize.SendSQLLog("DELETE FROM comments WHERE replycid = '%s'", cid); err != nil {
			initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
		}

		// 更新文章评论数
		commentCount := len(replies) + 1 // 二级评论数量加上一级评论本身
		if err := tx.Model(&model.Article{}).Where("aid = ?", comment.Target).UpdateColumn("comments", gorm.Expr("comments - ?", commentCount)).Error; err != nil {
			tx.Rollback()
			return err
		}
		// 发送SQL日志到Kafka
		if err := initialize.SendSQLLog("UPDATE articles SET comments = comments - %d WHERE aid = '%s'", commentCount, comment.Target); err != nil {
			initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
		}

		// 更新Redis中的评论数
		ctx := context.Background()
		if err := initialize.RDB.ZIncrBy(ctx, "article_comments", float64(-commentCount), comment.Target).Err(); err != nil {
			initialize.Logger.Error("Failed to update Redis article comments count", zap.Error(err))
		}
	}

	// 删除评论本身
	if err := tx.Delete(&comment).Error; err != nil {
		tx.Rollback()
		return err
	}
	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("DELETE FROM comments WHERE cid = '%s'", cid); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		return err
	}

	return nil
}
