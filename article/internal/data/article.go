package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/knoci/roaming-world/article/internal/biz"
)

const (
	keyArticleLikes     = "article:likes"
	keyArticleComments  = "article:comments"
	keyArticleFavorites = "article:favorites"
	keyArticleTime      = "article:time"
)

// Article 文章表模型。
type Article struct {
	AID       string    `gorm:"primaryKey;type:varchar(36);column:aid" json:"aid"`
	UID       string    `gorm:"type:varchar(36);column:uid;index" json:"uid"`
	Name      string    `gorm:"type:varchar(50);not null" json:"name"`
	Avatar    string    `gorm:"type:varchar(255)" json:"avatar"`
	Title     string    `gorm:"type:varchar(100);not null" json:"title"`
	View      []string  `gorm:"type:text;not null;serializer:json" json:"view"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	Likes     int       `gorm:"default:0" json:"likes"`
	Comments  int       `gorm:"default:0" json:"comments"`
	Favorite  int       `gorm:"default:0" json:"favorite"`
	Video     bool      `gorm:"default:false;index:idx_articles_video_videoid,priority:1" json:"video"`
	Videoid   int       `gorm:"default:0;index:idx_articles_video_videoid,priority:2" json:"videoid"`
	Height    int       `gorm:"default:0" json:"height"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	}
	if err := r.data.db.WithContext(ctx).Create(article).Error; err != nil {
		r.log.WithContext(ctx).Errorf("articleRepo: create article failed: %v", err)
		_ = r.data.SendErrorLog(ctx, "article", err.Error(), "db.Create", article)
		return nil, err
	}

	viewJSON, _ := json.Marshal(article.View)
	r.sendSQL(ctx, "INSERT INTO articles (aid, uid, name, avatar, title, view, content, video, videoid, height) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)",
		article.AID, article.UID, article.Name, article.Avatar, article.Title, string(viewJSON), article.Content, article.Video, article.Videoid, article.Height)

	// 新文章初始化四个排序 zset，后续点赞/评论/收藏只更新分数。
	pipe := r.data.redis.Pipeline()
	pipe.ZAdd(ctx, keyArticleLikes, redis.Z{Score: 0, Member: article.AID})
	pipe.ZAdd(ctx, keyArticleComments, redis.Z{Score: 0, Member: article.AID})
	pipe.ZAdd(ctx, keyArticleFavorites, redis.Z{Score: 0, Member: article.AID})
	pipe.ZAdd(ctx, keyArticleTime, redis.Z{Score: float64(article.CreatedAt.Unix()), Member: article.AID})
	if _, err := pipe.Exec(ctx); err != nil {
		r.log.WithContext(ctx).Errorf("articleRepo: init article cache failed: %v", err)
	}

	return toBizArticle(article), nil
}

func (r *articleRepo) Update(ctx context.Context, a *biz.Article) (*biz.Article, error) {
	var existing Article
	if err := r.data.db.WithContext(ctx).Where("aid = ?", a.AID).First(&existing).Error; err != nil {
		return nil, err
	}

	if a.Video != existing.Video {
		if a.Video {
			var max struct{ MaxID int }
			if err := r.data.db.WithContext(ctx).Model(&Article{}).
				Select("COALESCE(MAX(videoid), 0) AS max_id").
				Where("video = ?", true).Scan(&max).Error; err != nil {
				return nil, err
			}
			existing.Videoid = max.MaxID + 1
		} else {
			existing.Videoid = 0
		}
	}

	existing.Avatar = a.Avatar
	existing.Title = a.Title
	existing.View = a.View
	existing.Content = a.Content
	existing.Video = a.Video
	existing.Height = a.Height
	existing.UpdatedAt = time.Now()

	if err := r.data.db.WithContext(ctx).Save(&existing).Error; err != nil {
		r.log.WithContext(ctx).Errorf("articleRepo: update article failed: %v", err)
		_ = r.data.SendErrorLog(ctx, "article", err.Error(), "db.Save", existing)
		return nil, err
	}

	viewJSON, _ := json.Marshal(existing.View)
	r.sendSQL(ctx, "UPDATE articles SET title=$1, view=$2, content=$3, video=$4, videoid=$5, height=$6, avatar=$7, updated_at=NOW() WHERE aid=$8",
		existing.Title, string(viewJSON), existing.Content, existing.Video, existing.Videoid, existing.Height, existing.Avatar, existing.AID)
	_ = r.data.redis.ZAdd(ctx, keyArticleTime, redis.Z{Score: float64(existing.UpdatedAt.Unix()), Member: existing.AID}).Err()

	return toBizArticle(&existing), nil
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
		_ = r.data.SendErrorLog(ctx, "article", err.Error(), "db.Delete", aid)
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}

	r.sendSQL(ctx, "DELETE FROM likes WHERE aid=$1", aid)
	r.sendSQL(ctx, "DELETE FROM favorites WHERE aid=$1", aid)
	r.sendSQL(ctx, "DELETE FROM articles WHERE aid=$1", aid)

	pipe := r.data.redis.Pipeline()
	pipe.ZRem(ctx, keyArticleLikes, aid)
	pipe.ZRem(ctx, keyArticleComments, aid)
	pipe.ZRem(ctx, keyArticleFavorites, aid)
	pipe.ZRem(ctx, keyArticleTime, aid)
	if _, err := pipe.Exec(ctx); err != nil {
		r.log.WithContext(ctx).Errorf("articleRepo: remove article cache failed: %v", err)
	}
	return nil
}

func (r *articleRepo) FindByID(ctx context.Context, aid string) (*biz.Article, error) {
	var article Article
	if err := r.data.db.WithContext(ctx).Where("aid = ?", aid).First(&article).Error; err != nil {
		return nil, err
	}
	return toBizArticle(&article), nil
}

func (r *articleRepo) List(ctx context.Context, page, limit int, way string, reverse bool) ([]*biz.Article, int64, error) {
	key, orderBy, err := sortKey(way)
	if err != nil {
		return nil, 0, err
	}

	start := int64((page - 1) * limit)
	end := int64(page*limit - 1)
	if limit == 0 {
		start, end = 0, -1
	}

	var zs []redis.Z
	if reverse {
		zs, err = r.data.redis.ZRevRangeWithScores(ctx, key, start, end).Result()
	} else {
		zs, err = r.data.redis.ZRangeWithScores(ctx, key, start, end).Result()
	}

	if err == nil && len(zs) > 0 {
		aids := make([]string, 0, len(zs))
		for _, z := range zs {
			if aid, ok := z.Member.(string); ok {
				aids = append(aids, aid)
			}
		}
		articles, loadErr := r.loadByIDs(ctx, aids)
		if loadErr == nil {
			total, _ := r.data.redis.ZCard(ctx, key).Result()
			return articles, total, nil
		}
		r.log.WithContext(ctx).Warnf("articleRepo: redis list hit but load failed: %v", loadErr)
	}

	// 缓存未命中或 Redis 异常：回源数据库，并重建对应 zset。
	query := r.data.db.WithContext(ctx).Model(&Article{})
	var total int64
	if err := query.Count(&total).Error; err != nil {
		_ = r.data.SendErrorLog(ctx, "article", err.Error(), "db.Count", nil)
		return nil, 0, err
	}

	var rows []Article
	q := r.data.db.WithContext(ctx).Model(&Article{}).Order(orderBy)
	if limit > 0 {
		q = q.Offset(int(start)).Limit(limit)
	}
	if err := q.Find(&rows).Error; err != nil {
		_ = r.data.SendErrorLog(ctx, "article", err.Error(), "db.Find", nil)
		return nil, 0, err
	}

	articles := make([]*biz.Article, 0, len(rows))
	for i := range rows {
		articles = append(articles, toBizArticle(&rows[i]))
	}
	if limit == 0 {
		r.rebuildCache(ctx, key, articles, way)
	}
	return articles, total, nil
}

func (r *articleRepo) Like(ctx context.Context, uid, aid string) (bool, int, error) {
	tx := r.data.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return false, 0, tx.Error
	}
	defer func() {
		if rec := recover(); rec != nil {
			tx.Rollback()
		}
	}()

	var like Like
	err := tx.Where("uid = ? AND aid = ?", uid, aid).First(&like).Error
	liked := false
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := tx.Create(&Like{UID: uid, AID: aid}).Error; err != nil {
			tx.Rollback()
			return false, 0, err
		}
		if err := tx.Model(&Article{}).Where("aid = ?", aid).UpdateColumn("likes", gorm.Expr("likes + 1")).Error; err != nil {
			tx.Rollback()
			return false, 0, err
		}
		liked = true
		r.sendSQL(ctx, "INSERT INTO likes (uid, aid) VALUES ($1,$2)", uid, aid)
		r.sendSQL(ctx, "UPDATE articles SET likes = likes + 1 WHERE aid=$1", aid)
	case err != nil:
		tx.Rollback()
		return false, 0, err
	default:
		if err := tx.Delete(&like).Error; err != nil {
			tx.Rollback()
			return false, 0, err
		}
		if err := tx.Model(&Article{}).Where("aid = ?", aid).UpdateColumn("likes", gorm.Expr("GREATEST(likes - 1, 0)")).Error; err != nil {
			tx.Rollback()
			return false, 0, err
		}
		r.sendSQL(ctx, "DELETE FROM likes WHERE uid=$1 AND aid=$2", uid, aid)
		r.sendSQL(ctx, "UPDATE articles SET likes = GREATEST(likes - 1, 0) WHERE aid=$1", aid)
	}

	if err := tx.Commit().Error; err != nil {
		return false, 0, err
	}

	delta := -1
	if liked {
		delta = 1
	}
	if err := r.data.redis.ZIncrBy(ctx, keyArticleLikes, float64(delta), aid).Err(); err != nil {
		r.log.WithContext(ctx).Warnf("articleRepo: update like cache failed: %v", err)
	}

	var row struct{ Likes int }
	_ = r.data.db.WithContext(ctx).Model(&Article{}).Where("aid = ?", aid).Select("likes").Scan(&row).Error
	return liked, row.Likes, nil
}

func (r *articleRepo) Favorite(ctx context.Context, uid, aid string) (bool, int, error) {
	tx := r.data.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return false, 0, tx.Error
	}
	defer func() {
		if rec := recover(); rec != nil {
			tx.Rollback()
		}
	}()

	var fav Favorite
	err := tx.Where("uid = ? AND aid = ?", uid, aid).First(&fav).Error
	favorited := false
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := tx.Create(&Favorite{UID: uid, AID: aid}).Error; err != nil {
			tx.Rollback()
			return false, 0, err
		}
		if err := tx.Model(&Article{}).Where("aid = ?", aid).UpdateColumn("favorite", gorm.Expr("favorite + 1")).Error; err != nil {
			tx.Rollback()
			return false, 0, err
		}
		favorited = true
		r.sendSQL(ctx, "INSERT INTO favorites (uid, aid) VALUES ($1,$2)", uid, aid)
		r.sendSQL(ctx, "UPDATE articles SET favorite = favorite + 1 WHERE aid=$1", aid)
	case err != nil:
		tx.Rollback()
		return false, 0, err
	default:
		if err := tx.Delete(&fav).Error; err != nil {
			tx.Rollback()
			return false, 0, err
		}
		if err := tx.Model(&Article{}).Where("aid = ?", aid).UpdateColumn("favorite", gorm.Expr("GREATEST(favorite - 1, 0)")).Error; err != nil {
			tx.Rollback()
			return false, 0, err
		}
		r.sendSQL(ctx, "DELETE FROM favorites WHERE uid=$1 AND aid=$2", uid, aid)
		r.sendSQL(ctx, "UPDATE articles SET favorite = GREATEST(favorite - 1, 0) WHERE aid=$1", aid)
	}

	if err := tx.Commit().Error; err != nil {
		return false, 0, err
	}

	delta := -1
	if favorited {
		delta = 1
	}
	if err := r.data.redis.ZIncrBy(ctx, keyArticleFavorites, float64(delta), aid).Err(); err != nil {
		r.log.WithContext(ctx).Warnf("articleRepo: update favorite cache failed: %v", err)
	}

	var row struct{ Favorite int }
	_ = r.data.db.WithContext(ctx).Model(&Article{}).Where("aid = ?", aid).Select("favorite").Scan(&row).Error
	return favorited, row.Favorite, nil
}

func (r *articleRepo) GetUserLikeList(ctx context.Context, uid string) ([]string, error) {
	var likes []Like
	if err := r.data.db.WithContext(ctx).Where("uid = ?", uid).Find(&likes).Error; err != nil {
		return nil, err
	}
	aids := make([]string, 0, len(likes))
	for _, l := range likes {
		aids = append(aids, l.AID)
	}
	return aids, nil
}

func (r *articleRepo) GetUserFavoriteList(ctx context.Context, uid string) ([]string, error) {
	var favorites []Favorite
	if err := r.data.db.WithContext(ctx).Where("uid = ?", uid).Find(&favorites).Error; err != nil {
		return nil, err
	}
	aids := make([]string, 0, len(favorites))
	for _, f := range favorites {
		aids = append(aids, f.AID)
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

func (r *articleRepo) loadByIDs(ctx context.Context, aids []string) ([]*biz.Article, error) {
	if len(aids) == 0 {
		return []*biz.Article{}, nil
	}
	var rows []Article
	if err := r.data.db.WithContext(ctx).Where("aid IN ?", aids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]*Article, len(rows))
	for i := range rows {
		byID[rows[i].AID] = &rows[i]
	}
	result := make([]*biz.Article, 0, len(aids))
	for _, aid := range aids {
		if row, ok := byID[aid]; ok {
			result = append(result, toBizArticle(row))
		}
	}
	return result, nil
}

func (r *articleRepo) rebuildCache(ctx context.Context, key string, articles []*biz.Article, way string) {
	if len(articles) == 0 {
		return
	}
	pipe := r.data.redis.Pipeline()
	pipe.Del(ctx, key)
	for _, a := range articles {
		score := float64(0)
		switch way {
		case "likes":
			score = float64(a.Likes)
		case "comments":
			score = float64(a.Comments)
		case "favorites":
			score = float64(a.Favorite)
		case "time":
			score = float64(a.CreatedAt.Unix())
		}
		pipe.ZAdd(ctx, key, redis.Z{Score: score, Member: a.AID})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		r.log.WithContext(ctx).Warnf("articleRepo: rebuild article cache failed: %v", err)
	}
}

func (r *articleRepo) sendSQL(ctx context.Context, sql string, params ...any) {
	if err := r.data.SendSqlLog(ctx, "article", sql, params); err != nil {
		r.log.WithContext(ctx).Errorf("articleRepo: kafka send sqllog error: %v", err)
	}
}

func sortKey(way string) (string, string, error) {
	switch way {
	case "likes":
		return keyArticleLikes, "likes", nil
	case "comments":
		return keyArticleComments, "comments", nil
	case "favorites":
		return keyArticleFavorites, "favorite", nil
	case "time", "":
		return keyArticleTime, "created_at", nil
	default:
		return "", "", fmt.Errorf("invalid way: %s", way)
	}
}

func toBizArticle(a *Article) *biz.Article {
	return &biz.Article{
		AID:       a.AID,
		UID:       a.UID,
		Name:      a.Name,
		Avatar:    a.Avatar,
		Title:     a.Title,
		View:      a.View,
		Content:   a.Content,
		Likes:     a.Likes,
		Comments:  a.Comments,
		Favorite:  a.Favorite,
		Video:     a.Video,
		Videoid:   a.Videoid,
		Height:    a.Height,
		CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt,
	}
}
