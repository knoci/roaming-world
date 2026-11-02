package biz

import (
	"context"
	"errors"
	"strings"
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
	NotifyUID string
	Status    string
	DeletedAt *time.Time
	Time      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CommentReport 评论举报。
type CommentReport struct {
	ID          string
	CID         string
	ReporterUID string
	Reason      string
	Status      string
	ReviewNote  string
	CreatedAt   time.Time
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
	Unlike(ctx context.Context, uid, cid string) error
	FindComment(ctx context.Context, cid string) (*Comment, error)
	GetCommentList(ctx context.Context, aid string, page, limit int) ([]*Comment, int64, error)
	GetReplyList(ctx context.Context, cid string, page, limit int) ([]*Comment, int64, error)
	GetCommentListWithReplies(ctx context.Context, aid string, page, limit int) ([]*Comment, int64, error)
	CreateReport(ctx context.Context, report *CommentReport) (*CommentReport, error)
	ListReports(ctx context.Context, status string, page, limit int) ([]*CommentReport, int64, error)
	ResolveReport(ctx context.Context, id, adminID, action, note string) (*CommentReport, error)
	FindUser(ctx context.Context, uid string) (*User, error)
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

	// 回复：父评论必须存在、未删除，且属于同一目标；只保留两级。
	notifyUID := ""
	if replycid != "" {
		parentComment, err := uc.repo.FindComment(ctx, replycid)
		if err != nil || parentComment.Status != "active" || parentComment.Target != target {
			return nil, errors.New("回复的评论不存在")
		}
		replyname = parentComment.Name
		notifyUID = parentComment.UID
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
		NotifyUID: notifyUID,
		Status:    "active",
		Time:      time.Now().Format("2006-01-02 15:04:05"),
		Likes:     0,
	}

	// 保存评论
	createdComment, err := uc.repo.Create(ctx, comment)
	if err != nil {
		return nil, err
	}

	return createdComment, nil
}

// DeleteComment 删除评论
func (uc *CommentUsecase) DeleteComment(ctx context.Context, uid, cid string) error {
	return uc.repo.Delete(ctx, uid, cid)
}

// LikeComment 点赞评论
func (uc *CommentUsecase) LikeComment(ctx context.Context, uid, cid string) error {
	comment, err := uc.repo.FindComment(ctx, cid)
	if err != nil || comment.Status != "active" {
		return ErrCommentNotFound
	}
	return uc.repo.Like(ctx, uid, cid)
}

func (uc *CommentUsecase) UnlikeComment(ctx context.Context, uid, cid string) error {
	comment, err := uc.repo.FindComment(ctx, cid)
	if err != nil || comment.Status != "active" {
		return ErrCommentNotFound
	}
	return uc.repo.Unlike(ctx, uid, cid)
}

// GetCommentList 获取一级评论列表
func (uc *CommentUsecase) GetCommentList(ctx context.Context, aid string, page, limit int) ([]*Comment, int64, error) {
	page, limit = normalizePage(page, limit)
	return uc.repo.GetCommentList(ctx, aid, page, limit)
}

// GetReplyList 获取二级评论列表
func (uc *CommentUsecase) GetReplyList(ctx context.Context, cid string, page, limit int) ([]*Comment, int64, error) {
	page, limit = normalizePage(page, limit)
	return uc.repo.GetReplyList(ctx, cid, page, limit)
}

// GetCommentListWithReplies 获取一级评论列表及其前两条二级评论
func (uc *CommentUsecase) GetCommentListWithReplies(ctx context.Context, aid string, page, limit int) ([]*Comment, int64, error) {
	page, limit = normalizePage(page, limit)
	return uc.repo.GetCommentListWithReplies(ctx, aid, page, limit)
}

func normalizePage(page, limit int) (int, int) {
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

func (uc *CommentUsecase) ReportComment(ctx context.Context, uid, cid, reason string) (*CommentReport, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return nil, ErrInvalidArgument
	}
	comment, err := uc.repo.FindComment(ctx, cid)
	if err != nil || comment.Status != "active" {
		return nil, ErrCommentNotFound
	}
	if comment.UID == uid {
		return nil, ErrInvalidArgument
	}
	report, err := uc.repo.CreateReport(ctx, &CommentReport{
		CID: cid, ReporterUID: uid, Reason: reason, Status: "pending",
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

func (uc *CommentUsecase) ListReports(ctx context.Context, status string, page, limit int) ([]*CommentReport, int64, error) {
	if status == "" {
		status = "pending"
	}
	switch status {
	case "pending", "resolved", "rejected":
	default:
		return nil, 0, ErrInvalidArgument
	}
	page, limit = normalizePage(page, limit)
	return uc.repo.ListReports(ctx, status, page, limit)
}

func (uc *CommentUsecase) ResolveReport(ctx context.Context, adminID, id, action, note string) (*CommentReport, error) {
	if action != "remove" && action != "dismiss" {
		return nil, ErrInvalidArgument
	}
	return uc.repo.ResolveReport(ctx, id, adminID, action, note)
}
