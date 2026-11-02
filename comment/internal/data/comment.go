package data

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	pd "github.com/knoci/roaming-world/comment/api/user/v1"
	"github.com/knoci/roaming-world/comment/internal/biz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Comment 评论模型
type Comment struct {
	CID       string     `gorm:"primaryKey;type:varchar(36);column:cid" json:"cid"`
	Target    string     `gorm:"type:varchar(36);index:idx_comments_target_reply_time,priority:1" json:"target"`
	Content   string     `gorm:"type:text;not null" json:"content"`
	Likes     int        `gorm:"default:0" json:"likes"`
	UID       string     `gorm:"type:varchar(36);column:uid;index:idx_comments_uid" json:"uid"`
	Name      string     `gorm:"type:varchar(50);not null" json:"name"`
	Avatar    string     `gorm:"type:varchar(255)" json:"avatar"`
	Replycid  string     `gorm:"type:varchar(36);index:idx_comments_target_reply_time,priority:2;index:idx_comments_reply_time,priority:1" json:"replycid"`
	Replyname string     `gorm:"type:varchar(50)" json:"replyname"`
	Status    string     `gorm:"type:varchar(20);not null;default:active;index:idx_comments_status" json:"status"`
	DeletedAt *time.Time `json:"deleted_at"`
	Time      string     `gorm:"-" json:"time"`
	CreatedAt time.Time  `gorm:"index:idx_comments_target_reply_time,priority:3;index:idx_comments_reply_time,priority:2" json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
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
	UID       string    `gorm:"type:varchar(36);column:uid;uniqueIndex:uk_commentlike_uid_cid,priority:1" json:"uid"`
	CID       string    `gorm:"type:varchar(36);column:cid;uniqueIndex:uk_commentlike_uid_cid,priority:2" json:"cid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CommentReport struct {
	ID          string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	CID         string    `gorm:"type:varchar(36);not null;uniqueIndex:uk_comment_report_user,priority:1;index:idx_comment_reports_cid" json:"cid"`
	ReporterUID string    `gorm:"type:varchar(36);not null;uniqueIndex:uk_comment_report_user,priority:2" json:"reporter_uid"`
	Reason      string    `gorm:"type:varchar(500);not null" json:"reason"`
	Status      string    `gorm:"type:varchar(20);not null;default:pending;index:idx_comment_reports_status" json:"status"`
	ReviewedBy  string    `gorm:"type:varchar(36)" json:"reviewed_by"`
	ReviewNote  string    `gorm:"type:text" json:"review_note"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
		Status:    "active",
		Likes:     comment.Likes,
	}

	if err := r.data.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(c).Error; err != nil {
			return err
		}
		return r.data.enqueueCommentCount(tx, c.Target)
	}); err != nil {
		r.log.WithContext(ctx).Errorf("commentRepo: create comment error: %v", err)
		_ = r.data.LogError(ctx, "comment", err.Error(), "db.Create", c)
		return nil, err
	}

	if comment.NotifyUID != "" && comment.NotifyUID != c.UID {
		if _, err := r.data.userClient.CreateNotification(ctx, &pd.CreateNotificationRequest{
			Uid: comment.NotifyUID, Type: "comment_reply", ActorUid: c.UID, ActorName: c.Name, Payload: c.CID,
		}); err != nil {
			r.log.WithContext(ctx).Errorf("commentRepo: create reply notification failed: %v", err)
		}
	}

	c.Time = formatCommentTime(c.CreatedAt)
	sql := `INSERT INTO comments (cid, target, content, uid, name, avatar, replycid, replyname, likes) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	params := []any{c.CID, c.Target, c.Content, c.UID, c.Name, c.Avatar, c.Replycid, c.Replyname, c.Likes}
	error := r.data.LogSQLAudit(ctx, "comment", sql, params)
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
		Status:    c.Status,
		DeletedAt: c.DeletedAt,
		Time:      formatCommentTime(c.CreatedAt),
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}, nil
}

// Delete 软删除评论：保留行用于审计和回复链，列表只展示 active。
func (r *commentRepo) Delete(ctx context.Context, uid, cid string) error {
	var comment Comment
	if err := r.data.db.WithContext(ctx).Where("cid = ?", cid).First(&comment).Error; err != nil {
		return biz.ErrCommentNotFound
	}
	if comment.UID != uid {
		return biz.ErrNoPermission
	}
	if comment.Status == "deleted" {
		return nil
	}

	now := time.Now()
	if err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if comment.Replycid == "" {
			if err := tx.Model(&Comment{}).Where("replycid = ? AND status = ?", cid, "active").Updates(map[string]any{
				"status": "deleted", "content": "该评论已删除", "deleted_at": now,
			}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&Comment{}).Where("cid = ?", cid).Updates(map[string]any{
			"status": "deleted", "content": "该评论已删除", "deleted_at": now,
		}).Error; err != nil {
			return err
		}
		return r.data.enqueueCommentCount(tx, comment.Target)
	}); err != nil {
		r.log.WithContext(ctx).Errorf("commentRepo: delete comment error: %v", err)
		return err
	}
	return nil
}

// Like 点赞评论：幂等，重复点赞不重复加计数。
func (r *commentRepo) Like(ctx context.Context, uid, cid string) error {
	res := r.data.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&Commentlike{UID: uid, CID: cid})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return r.data.db.WithContext(ctx).Model(&Comment{}).Where("cid = ?", cid).
			UpdateColumn("likes", gorm.Expr("likes + 1")).Error
	}
	return nil
}

// Unlike 取消点赞：幂等，没点过也返回成功。
func (r *commentRepo) Unlike(ctx context.Context, uid, cid string) error {
	res := r.data.db.WithContext(ctx).Where("uid = ? AND cid = ?", uid, cid).Delete(&Commentlike{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return r.data.db.WithContext(ctx).Model(&Comment{}).Where("cid = ?", cid).
			UpdateColumn("likes", gorm.Expr("GREATEST(likes - 1, 0)")).Error
	}
	return nil
}

// GetCommentList 获取一级评论列表（分页）。
// FindComment 按 CID 查一条评论。
func (r *commentRepo) FindComment(ctx context.Context, cid string) (*biz.Comment, error) {
	var c Comment
	if err := r.data.db.WithContext(ctx).Where("cid = ?", cid).First(&c).Error; err != nil {
		return nil, err
	}
	return toBizComment(&c), nil
}

func (r *commentRepo) GetCommentList(ctx context.Context, aid string, page, limit int) ([]*biz.Comment, int64, error) {
	q := r.data.db.WithContext(ctx).Model(&Comment{}).Where("target = ? AND (replycid = '' OR replycid IS NULL) AND status = ?", aid, "active")
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var comments []Comment
	if err := q.Order("created_at DESC").Offset((page - 1) * limit).Limit(limit).Find(&comments).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.Comment, 0, len(comments))
	for i := range comments {
		result = append(result, toBizComment(&comments[i]))
	}
	return result, total, nil
}

// GetReplyList 获取二级评论列表（分页，严格两级）。
func (r *commentRepo) GetReplyList(ctx context.Context, cid string, page, limit int) ([]*biz.Comment, int64, error) {
	q := r.data.db.WithContext(ctx).Model(&Comment{}).Where("replycid = ? AND status = ?", cid, "active")
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var comments []Comment
	if err := q.Order("created_at DESC").Offset((page - 1) * limit).Limit(limit).Find(&comments).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.Comment, 0, len(comments))
	for i := range comments {
		result = append(result, toBizComment(&comments[i]))
	}
	return result, total, nil
}

// GetCommentListWithReplies 获取一级评论 + 每条前两条回复，根评论分页。
func (r *commentRepo) GetCommentListWithReplies(ctx context.Context, aid string, page, limit int) ([]*biz.Comment, int64, error) {
	q := r.data.db.WithContext(ctx).Model(&Comment{}).Where("target = ? AND (replycid = '' OR replycid IS NULL) AND status = ?", aid, "active")
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var roots []Comment
	if err := q.Order("created_at DESC").Offset((page - 1) * limit).Limit(limit).Find(&roots).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.Comment, 0, len(roots))
	if len(roots) == 0 {
		return result, total, nil
	}
	ids := make([]string, 0, len(roots))
	for i := range roots {
		result = append(result, toBizComment(&roots[i]))
		ids = append(ids, roots[i].CID)
	}
	var replies []Comment
	if err := r.data.db.WithContext(ctx).Where("replycid IN ? AND status = ?", ids, "active").
		Order("created_at DESC").Find(&replies).Error; err != nil {
		return nil, 0, err
	}
	byRoot := make(map[string][]Comment)
	for i := range replies {
		if len(byRoot[replies[i].Replycid]) >= 2 {
			continue
		}
		byRoot[replies[i].Replycid] = append(byRoot[replies[i].Replycid], replies[i])
	}
	for i := range roots {
		for _, reply := range byRoot[roots[i].CID] {
			result = append(result, toBizComment(&reply))
		}
	}
	return result, total, nil
}

func toBizComment(c *Comment) *biz.Comment {
	return &biz.Comment{
		CID: c.CID, Target: c.Target, Content: c.Content, Likes: c.Likes,
		UID: c.UID, Name: c.Name, Avatar: c.Avatar,
		Replycid: c.Replycid, Replyname: c.Replyname,
		Status: c.Status, DeletedAt: c.DeletedAt,
		Time: formatCommentTime(c.CreatedAt), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

func (r *commentRepo) CreateReport(ctx context.Context, report *biz.CommentReport) (*biz.CommentReport, error) {
	var count int64
	if err := r.data.db.WithContext(ctx).Model(&CommentReport{}).
		Where("cid = ? AND reporter_uid = ?", report.CID, report.ReporterUID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, biz.ErrReportExists
	}
	row := &CommentReport{
		ID: uuid.New().String(), CID: report.CID, ReporterUID: report.ReporterUID,
		Reason: report.Reason, Status: "pending",
	}
	if err := r.data.db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, err
	}
	return toBizReport(row), nil
}

func (r *commentRepo) ListReports(ctx context.Context, status string, page, limit int) ([]*biz.CommentReport, int64, error) {
	q := r.data.db.WithContext(ctx).Model(&CommentReport{}).Where("status = ?", status)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []CommentReport
	if err := q.Order("created_at DESC").Offset((page - 1) * limit).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.CommentReport, 0, len(rows))
	for i := range rows {
		result = append(result, toBizReport(&rows[i]))
	}
	return result, total, nil
}

func (r *commentRepo) ResolveReport(ctx context.Context, id, adminID, action, note string) (*biz.CommentReport, error) {
	var report CommentReport
	if err := r.data.db.WithContext(ctx).Where("id = ?", id).First(&report).Error; err != nil {
		return nil, biz.ErrReportNotFound
	}
	if report.Status != "pending" {
		return toBizReport(&report), nil
	}
	if err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if action == "remove" {
			var comment Comment
			if err := tx.Where("cid = ?", report.CID).First(&comment).Error; err != nil {
				return biz.ErrCommentNotFound
			}
			now := time.Now()
			if comment.Replycid == "" {
				if err := tx.Model(&Comment{}).Where("replycid = ? AND status = ?", comment.CID, "active").Updates(map[string]any{
					"status": "deleted", "content": "该评论已删除", "deleted_at": now,
				}).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&Comment{}).Where("cid = ?", comment.CID).Updates(map[string]any{
				"status": "deleted", "content": "该评论已删除", "deleted_at": now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&CommentReport{}).Where("cid = ? AND status = ?", comment.CID, "pending").Updates(map[string]any{
				"status": "resolved", "reviewed_by": adminID, "review_note": note,
			}).Error; err != nil {
				return err
			}
			if err := r.data.enqueueCommentCount(tx, comment.Target); err != nil {
				return err
			}
		} else {
			if err := tx.Model(&CommentReport{}).Where("id = ?", id).Updates(map[string]any{
				"status": "rejected", "reviewed_by": adminID, "review_note": note,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := r.data.db.WithContext(ctx).Where("id = ?", id).First(&report).Error; err != nil {
		return nil, err
	}
	return toBizReport(&report), nil
}

func toBizReport(row *CommentReport) *biz.CommentReport {
	return &biz.CommentReport{
		ID: row.ID, CID: row.CID, ReporterUID: row.ReporterUID,
		Reason: row.Reason, Status: row.Status, ReviewNote: row.ReviewNote, CreatedAt: row.CreatedAt,
	}
}

// FindUser 查找用户
func (r *commentRepo) FindUser(ctx context.Context, uid string) (*biz.User, error) {
	rsp, err := r.data.userClient.FindUser(ctx, &pd.FindUserRequest{Keyword: uid})
	if err != nil {
		r.log.WithContext(ctx).Errorf("commentRepo: call user.FindUser failed: %v", err)
		return nil, biz.ErrInternalError
	}

	return &biz.User{
		UID:    rsp.Uid,
		Name:   rsp.Name,
		Avatar: rsp.Avatar,
		Email:  rsp.Email,
	}, nil
}

// formatCommentTime 只用于兼容旧 API 的 time 字段，真实时间以 created_at 为准。
func formatCommentTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
