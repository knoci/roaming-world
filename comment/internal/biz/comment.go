package biz

import (
	"context"
	"errors"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

// Comment 评论实体
type Comment struct {
	CID       string
	Target    string
	Content   string
	Likes     int
	UID       string
	Name      string
	Avatar    string
	Replycid  string
	Replyname string
	Time      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// User 用户实体
type User struct {
	UID    string
	Name   string
	Avatar string
	Email  string
}

// Article 文章实体
type Article struct {
	AID      string
	Comments int
}

// CommentRepo 评论仓库接口
type CommentRepo interface {
	Create(ctx context.Context, comment *Comment) (*Comment, error)
	Delete(ctx context.Context, uid, cid string) error
	Like(ctx context.Context, uid, cid string) error
	GetCommentList(ctx context.Context, aid string) ([]*Comment, error)
	GetReplyList(ctx context.Context, cid string) ([]*Comment, error)
	GetCommentListWithReplies(ctx context.Context, aid string) ([]*Comment, error)
	FindUser(ctx context.Context, uid string) (*User, error)
	UpdateArticleCommentCount(ctx context.Context, aid string, count int) error
	UpdateRedisArticleCommentCount(ctx context.Context, aid string, count float64) error
}

// CommentUsecase 评论用例
type CommentUsecase struct {
	repo CommentRepo
	log  *log.Helper
}

// NewCommentUsecase 创建评论用例
func NewCommentUsecase(repo CommentRepo, logger log.Logger) *CommentUsecase {
	return &CommentUsecase{repo: repo, log: log.NewHelper(logger)}
}

// CreateComment 创建评论
func (uc *CommentUsecase) CreateComment(ctx context.Context, uid, target, content, replycid, replyname string) (*Comment, error) {
	// 获取用户信息
	user, err := uc.repo.FindUser(ctx, uid)
	if err != nil {
		return nil, errors.New("用户不存在")
	}

	// 如果有replycid，检查是否为二级评论
	if replycid != "" {
		var parentComment *Comment
		parentComments, err := uc.repo.GetCommentList(ctx, target)
		if err != nil {
			return nil, err
		}

		// 查找父评论
		for _, c := range parentComments {
			if c.CID == replycid {
				parentComment = c
				break
			}
		}

		if parentComment == nil {
			return nil, errors.New("回复的评论不存在")
		}

		// 如果父评论是二级评论，使用其replycid
		if parentComment.Replycid != "" {
			replycid = parentComment.Replycid
		}
	}

	// 创建评论
	comment := &Comment{
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

	// 保存评论
	createdComment, err := uc.repo.Create(ctx, comment)
	if err != nil {
		return nil, err
	}

	// 更新文章评论数
	if replycid == "" {
		// 更新数据库中的评论数
		if err := uc.repo.UpdateArticleCommentCount(ctx, target, 1); err != nil {
			uc.log.WithContext(ctx).Errorf("Failed to update article comments count: %v", err)
		}

		// 更新Redis中的评论数
		if err := uc.repo.UpdateRedisArticleCommentCount(ctx, target, 1); err != nil {
			uc.log.WithContext(ctx).Errorf("Failed to update Redis article comments count: %v", err)
		}
	}

	return createdComment, nil
}

// DeleteComment 删除评论
func (uc *CommentUsecase) DeleteComment(ctx context.Context, uid, cid string) error {
	return uc.repo.Delete(ctx, uid, cid)
}

// LikeComment 点赞评论
func (uc *CommentUsecase) LikeComment(ctx context.Context, uid, cid string) error {
	return uc.repo.Like(ctx, uid, cid)
}

// GetCommentList 获取一级评论列表
func (uc *CommentUsecase) GetCommentList(ctx context.Context, aid string) ([]*Comment, error) {
	return uc.repo.GetCommentList(ctx, aid)
}

// GetReplyList 获取二级评论列表
func (uc *CommentUsecase) GetReplyList(ctx context.Context, cid string) ([]*Comment, error) {
	return uc.repo.GetReplyList(ctx, cid)
}

// GetCommentListWithReplies 获取一级评论列表及其前两条二级评论
func (uc *CommentUsecase) GetCommentListWithReplies(ctx context.Context, aid string) ([]*Comment, error) {
	return uc.repo.GetCommentListWithReplies(ctx, aid)
}
