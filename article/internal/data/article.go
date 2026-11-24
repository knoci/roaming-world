package data

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	userv1 "github.com/knoci/roaming-world/article/api/user/v1"
	"github.com/knoci/roaming-world/article/internal/biz"
)

// Article 文章表模型。
type Article struct {
	AID          string     `gorm:"primaryKey;type:varchar(36);column:aid" json:"aid"`
	UID          string     `gorm:"type:varchar(36);column:uid;index" json:"uid"`
	Name         string     `gorm:"type:varchar(50);not null" json:"name"`
	Avatar       string     `gorm:"type:varchar(255)" json:"avatar"`
	Title        string     `gorm:"type:varchar(100);not null" json:"title"`
	View         []string   `gorm:"type:text;not null;serializer:json" json:"view"`
	Content      string     `gorm:"type:text;not null" json:"content"`
	Likes        int        `gorm:"default:0" json:"likes"`
	Comments     int        `gorm:"default:0" json:"comments"`
	Favorite     int        `gorm:"default:0" json:"favorite"`
	Video        bool       `gorm:"default:false;index:idx_articles_video_videoid,priority:1" json:"video"`
	Videoid      int        `gorm:"default:0;index:idx_articles_video_videoid,priority:2" json:"videoid"`
	Height       int        `gorm:"default:0" json:"height"`
	Status       string     `gorm:"type:varchar(20);not null;default:published;index:idx_articles_status" json:"status"`
	PublishedAt  *time.Time `json:"published_at"`
	ReviewedBy   string     `gorm:"type:varchar(36)" json:"reviewed_by"`
	ReviewReason string     `gorm:"type:text" json:"review_reason"`
	Revision     int64      `gorm:"not null;default:1" json:"revision"`
	CreatedAt    time.Time  `gorm:"index:idx_articles_created_at" json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func (a *Article) BeforeCreate(tx *gorm.DB) error {
	if a.AID == "" {
		a.AID = uuid.New().String()
	}
	return nil
}

// Like 点赞关系表。
type Like struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UID       string    `gorm:"type:varchar(36);column:uid;uniqueIndex:uk_like_uid_aid,priority:1" json:"uid"`
	AID       string    `gorm:"type:varchar(36);column:aid;uniqueIndex:uk_like_uid_aid,priority:2" json:"aid"`
	CreatedAt time.Time `json:"created_at"`
}

// Favorite 收藏关系表。
type Favorite struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UID       string    `gorm:"type:varchar(36);column:uid;uniqueIndex:uk_favorite_uid_aid,priority:1" json:"uid"`
	AID       string    `gorm:"type:varchar(36);column:aid;uniqueIndex:uk_favorite_uid_aid,priority:2" json:"aid"`
	CreatedAt time.Time `json:"created_at"`
}

type articleRepo struct {
	data *Data
	log  *log.Helper
}

func NewArticleRepo(data *Data, logger log.Logger) biz.ArticleRepo {
	return &articleRepo{data: data, log: log.NewHelper(logger)}
}

func (r *articleRepo) Create(ctx context.Context, a *biz.Article) (*biz.Article, error) {
	article := &Article{
		UID:     a.UID,
		Name:    a.Name,
		Avatar:  a.Avatar,
		Title:   a.Title,
		View:    a.View,
		Content: a.Content,
		Video:   a.Video,
		Height:  a.Height,
		Status:  a.Status,
	}
	if article.Status == "" {
		article.Status = "draft"
	}
	if err := r.data.db.WithContext(ctx).Create(article).Error; err != nil {
		r.log.WithContext(ctx).Errorf("articleRepo: create article failed: %v", err)
		_ = r.data.LogError(ctx, "article", err.Error(), "db.Create", article)
		return nil, err
	}

	viewJSON, _ := json.Marshal(article.View)
	r.sendSQL(ctx, "INSERT INTO articles (aid, uid, name, avatar, title, view, content, video, videoid, height) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)",
		article.AID, article.UID, article.Name, article.Avatar, article.Title, string(viewJSON), article.Content, article.Video, article.Videoid, article.Height)

	return toBizArticle(article), nil
}

func (r *articleRepo) Update(ctx context.Context, a *biz.Article) (*biz.Article, error) {
	viewJSON, err := json.Marshal(a.View)
	if err != nil {
		return nil, err
	}
	var updated Article
	result := r.data.db.WithContext(ctx).Model(&updated).Clauses(clause.Returning{}).
		Where("aid = ? AND uid = ? AND status = ? AND revision = ?", a.AID, a.UID, a.Status, a.Revision).
		Updates(map[string]any{
			"avatar": a.Avatar, "title": a.Title, "view": string(viewJSON),
			"content": a.Content, "video": a.Video, "height": a.Height,
			"videoid": gorm.Expr(`CASE WHEN COALESCE(video, false) = ? THEN videoid
				WHEN ? THEN (SELECT COALESCE(MAX(videoid), 0) + 1 FROM articles WHERE video = true)
				ELSE 0 END`, a.Video, a.Video),
			"revision": gorm.Expr("revision + 1"),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, biz.ErrArticleChanged
	}
	r.sendSQL(ctx, "UPDATE articles SET title=$1, view=$2, content=$3, video=$4, videoid=$5, height=$6, avatar=$7, updated_at=NOW() WHERE aid=$8",
		updated.Title, string(viewJSON), updated.Content, updated.Video, updated.Videoid, updated.Height, updated.Avatar, updated.AID)
	return toBizArticle(&updated), nil
}

func (r *articleRepo) Delete(ctx context.Context, aid string) error {
	tx := r.data.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	if err := tx.Where("aid = ?", aid).Delete(&Like{}).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Where("aid = ?", aid).Delete(&Favorite{}).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Where("aid = ?", aid).Delete(&Article{}).Error; err != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("articleRepo: delete article failed: %v", err)
		_ = r.data.LogError(ctx, "article", err.Error(), "db.Delete", aid)
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}

	r.sendSQL(ctx, "DELETE FROM likes WHERE aid=$1", aid)
	r.sendSQL(ctx, "DELETE FROM favorites WHERE aid=$1", aid)
	r.sendSQL(ctx, "DELETE FROM articles WHERE aid=$1", aid)

	return nil
}

// UpdateStatus applies a transition only to the revision checked by the use case.
func (r *articleRepo) UpdateStatus(ctx context.Context, current *biz.Article, status, reviewedBy, reason string) error {
	fields := map[string]any{
		"status": status, "reviewed_by": reviewedBy, "review_reason": reason,
		"revision": gorm.Expr("revision + 1"),
	}
	if status == "published" {
		fields["published_at"] = time.Now()
	}
	result := r.data.db.WithContext(ctx).Model(&Article{}).
		Where("aid = ? AND status = ? AND revision = ?", current.AID, current.Status, current.Revision).
		Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return biz.ErrArticleChanged
	}
	return nil
}

// ListFeed 关注作者的已发布文章。作者列表来自 user 服务 gRPC。
func (r *articleRepo) ListFeed(ctx context.Context, uid string, page, limit int) ([]*biz.Article, int64, error) {
	rpcCtx := metadata.AppendToOutgoingContext(ctx, "x-roaming-service-token", os.Getenv("ARTICLE_USER_RPC_TOKEN"))
	rsp, err := r.data.userClient.GetFollowingUIDs(rpcCtx, &userv1.GetFollowingUIDsRequest{Uid: uid})
	if err != nil {
		r.log.WithContext(ctx).Errorf("articleRepo: get following uids failed: %v", err)
		return nil, 0, err
	}
	if len(rsp.Uids) == 0 {
		return []*biz.Article{}, 0, nil
	}
	q := r.data.db.WithContext(ctx).Model(&Article{}).
		Where("status = ? AND uid IN ?", "published", rsp.Uids)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Article
	if err := q.Order("created_at DESC").Offset((page - 1) * limit).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.Article, 0, len(rows))
	for i := range rows {
		result = append(result, toBizArticle(&rows[i]))
	}
	return result, total, nil
}

func (r *articleRepo) FindByID(ctx context.Context, aid string) (*biz.Article, error) {
	var article Article
	if err := r.data.db.WithContext(ctx).Where("aid = ?", aid).First(&article).Error; err != nil {
		return nil, err
	}
	return toBizArticle(&article), nil
}

func (r *articleRepo) List(ctx context.Context, page, limit int, way string, reverse bool, status, ownerID string) ([]*biz.Article, int64, error) {
	var order string
	switch way {
	case "", "time":
		order = "created_at"
	case "likes", "comments":
		order = way
	case "favorites":
		order = "favorite"
	default:
		return nil, 0, biz.ErrInvalidArgument
	}

	q := r.data.db.WithContext(ctx).Model(&Article{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if ownerID != "" {
		q = q.Where("uid = ?", ownerID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q = q.Order(clause.OrderBy{
		Columns: []clause.OrderByColumn{
			{Column: clause.Column{Name: order}, Desc: reverse},
			{Column: clause.Column{Name: "aid"}, Desc: reverse},
		},
	})
	if page < 1 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	q = q.Offset((page - 1) * limit).Limit(limit)
	var rows []Article
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	articles := make([]*biz.Article, 0, len(rows))
	for i := range rows {
		articles = append(articles, toBizArticle(&rows[i]))
	}
	return articles, total, nil
}

func (r *articleRepo) Like(ctx context.Context, uid, aid string) (bool, int, error) {
	var likes int
	var updated bool
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Like{UID: uid, AID: aid})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			if err := tx.Model(&Article{}).Where("aid = ?", aid).
				UpdateColumn("likes", gorm.Expr("likes + 1")).Error; err != nil {
				return err
			}
			updated = true
		}
		var err error
		likes, err = articleLikesTx(tx, aid)
		return err
	})
	if err != nil {
		return false, 0, err
	}
	if updated {
		r.sendSQL(ctx, "UPDATE articles SET likes = likes + 1 WHERE aid=$1", aid)
	}
	return true, likes, nil
}

func (r *articleRepo) Unlike(ctx context.Context, uid, aid string) (int, error) {
	var likes int
	var updated bool
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("uid = ? AND aid = ?", uid, aid).Delete(&Like{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			if err := tx.Model(&Article{}).Where("aid = ?", aid).
				UpdateColumn("likes", gorm.Expr("GREATEST(likes - 1, 0)")).Error; err != nil {
				return err
			}
			updated = true
		}
		var err error
		likes, err = articleLikesTx(tx, aid)
		return err
	})
	if err != nil {
		return 0, err
	}
	if updated {
		r.sendSQL(ctx, "UPDATE articles SET likes = GREATEST(likes - 1, 0) WHERE aid=$1", aid)
	}
	return likes, nil
}

func articleLikesTx(tx *gorm.DB, aid string) (int, error) {
	var row struct{ Likes int }
	if err := tx.Model(&Article{}).Where("aid = ?", aid).Select("likes").Scan(&row).Error; err != nil {
		return 0, err
	}
	return row.Likes, nil
}

func (r *articleRepo) Favorite(ctx context.Context, uid, aid string) (bool, int, error) {
	var favorite int
	var updated bool
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Favorite{UID: uid, AID: aid})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			if err := tx.Model(&Article{}).Where("aid = ?", aid).
				UpdateColumn("favorite", gorm.Expr("favorite + 1")).Error; err != nil {
				return err
			}
			updated = true
		}
		var err error
		favorite, err = articleFavoritesTx(tx, aid)
		return err
	})
	if err != nil {
		return false, 0, err
	}
	if updated {
		r.sendSQL(ctx, "UPDATE articles SET favorite = favorite + 1 WHERE aid=$1", aid)
	}
	return true, favorite, nil
}

func (r *articleRepo) Unfavorite(ctx context.Context, uid, aid string) (int, error) {
	var favorite int
	var updated bool
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("uid = ? AND aid = ?", uid, aid).Delete(&Favorite{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			if err := tx.Model(&Article{}).Where("aid = ?", aid).
				UpdateColumn("favorite", gorm.Expr("GREATEST(favorite - 1, 0)")).Error; err != nil {
				return err
			}
			updated = true
		}
		var err error
		favorite, err = articleFavoritesTx(tx, aid)
		return err
	})
	if err != nil {
		return 0, err
	}
	if updated {
		r.sendSQL(ctx, "UPDATE articles SET favorite = GREATEST(favorite - 1, 0) WHERE aid=$1", aid)
	}
	return favorite, nil
}

func articleFavoritesTx(tx *gorm.DB, aid string) (int, error) {
	var row struct{ Favorite int }
	if err := tx.Model(&Article{}).Where("aid = ?", aid).Select("favorite").Scan(&row).Error; err != nil {
		return 0, err
	}
	return row.Favorite, nil
}

func (r *articleRepo) GetUserLikeList(ctx context.Context, uid string) ([]string, error) {
	var aids []string
	if err := r.data.db.WithContext(ctx).
		Model(&Like{}).
		Joins("JOIN articles ON articles.aid = likes.aid").
		Where("likes.uid = ? AND articles.status = ?", uid, "published").
		Pluck("likes.aid", &aids).Error; err != nil {
		return nil, err
	}
	if aids == nil {
		aids = []string{}
	}
	return aids, nil
}

func (r *articleRepo) GetUserFavoriteList(ctx context.Context, uid string) ([]string, error) {
	var aids []string
	if err := r.data.db.WithContext(ctx).
		Model(&Favorite{}).
		Joins("JOIN articles ON articles.aid = favorites.aid").
		Where("favorites.uid = ? AND articles.status = ?", uid, "published").
		Pluck("favorites.aid", &aids).Error; err != nil {
		return nil, err
	}
	if aids == nil {
		aids = []string{}
	}
	return aids, nil
}

func (r *articleRepo) GetVideoArticle(ctx context.Context, videoid int) (*biz.Article, error) {
	var article Article
	if err := r.data.db.WithContext(ctx).Where("video = ? AND videoid = ?", true, videoid).First(&article).Error; err != nil {
		return nil, err
	}
	return toBizArticle(&article), nil
}

func (r *articleRepo) GetMaxVideoID(ctx context.Context) (int, error) {
	var max struct{ MaxID int }
	if err := r.data.db.WithContext(ctx).Model(&Article{}).
		Select("COALESCE(MAX(videoid), 0) AS max_id").
		Where("video = ?", true).Scan(&max).Error; err != nil {
		return 0, err
	}
	return max.MaxID, nil
}

func (r *articleRepo) sendSQL(ctx context.Context, sql string, params ...any) {
	if err := r.data.LogSQLAudit(ctx, "article", sql, params); err != nil {
		r.log.WithContext(ctx).Errorf("articleRepo: kafka send sqllog error: %v", err)
	}
}

func toBizArticle(a *Article) *biz.Article {
	return &biz.Article{
		AID:          a.AID,
		UID:          a.UID,
		Name:         a.Name,
		Avatar:       a.Avatar,
		Title:        a.Title,
		View:         a.View,
		Content:      a.Content,
		Likes:        a.Likes,
		Comments:     a.Comments,
		Favorite:     a.Favorite,
		Video:        a.Video,
		Videoid:      a.Videoid,
		Height:       a.Height,
		Status:       a.Status,
		PublishedAt:  a.PublishedAt,
		ReviewedBy:   a.ReviewedBy,
		ReviewReason: a.ReviewReason,
		Revision:     a.Revision,
		CreatedAt:    a.CreatedAt,
		UpdatedAt:    a.UpdatedAt,
	}
}
