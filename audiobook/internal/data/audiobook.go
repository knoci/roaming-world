package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	"github.com/knoci/roaming-world/audiobook/internal/biz"
)

const (
	audiobookListCacheKey     = "audiobook:list"
	audiobookDetailsKeyPrefix = "audiobook:details:"
	audiobookNotFoundSentinel = "__NOT_FOUND__"
	audiobookNotFoundTTL      = 60 * time.Second
)

// Audiobook 有声书模型
type Audiobook struct {
	BID         string    `gorm:"primaryKey;type:varchar(36);column:bid" json:"bid"`
	View        string    `gorm:"type:varchar(255)" json:"view"`
	Author      string    `gorm:"type:varchar(50);not null" json:"author"`
	Name        string    `gorm:"type:varchar(50);not null" json:"name"`
	Playcount   int       `gorm:"default:0" json:"playcount"`
	Chapternum  int       `gorm:"default:0" json:"chapternum"`
	Rating      float64   `gorm:"default:9.4" json:"rating"`
	Description string    `gorm:"type:text" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (b *Audiobook) BeforeCreate(tx *gorm.DB) error {
	if b.BID == "" {
		b.BID = uuid.New().String()
	}
	return nil
}

// AudiobookDetail 有声书章节模型
type AudiobookDetail struct {
	DID       string    `gorm:"primaryKey;type:varchar(36);column:did;not null" json:"did"`
	BID       string    `gorm:"type:varchar(36);column:bid;index:idx_audiobook_details_bid_chapter,priority:1" json:"bid"`
	Chapter   int       `gorm:"default:1;not null;index:idx_audiobook_details_bid_chapter,priority:2" json:"chapter"`
	Audio     string    `gorm:"type:varchar(255);not null" json:"audio"`
	Name      string    `gorm:"type:varchar(255);not null" json:"name"`
	Duration  int       `gorm:"not null" json:"duration"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (d *AudiobookDetail) BeforeCreate(tx *gorm.DB) error {
	if d.DID == "" {
		d.DID = uuid.New().String()
	}
	return nil
}

type audiobookRepo struct {
	data *Data
	log  *log.Helper
	sf   singleflight.Group
}

// NewAudiobookRepo 创建有声书仓库实例
func NewAudiobookRepo(data *Data, logger log.Logger) biz.AudiobookRepo {
	return &audiobookRepo{
		data: data,
		log:  log.NewHelper(logger),
	}
}

// CreateAudiobook 创建有声书
func (r *audiobookRepo) CreateAudiobook(ctx context.Context, a *biz.Audiobook) (*biz.Audiobook, error) {
	audiobook := &Audiobook{
		View:        a.View,
		Author:      a.Author,
		Name:        a.Name,
		Playcount:   a.Playcount,
		Chapternum:  a.Chapternum,
		Rating:      a.Rating,
		Description: a.Description,
	}

	// 开启事务
	tx := r.data.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		r.log.WithContext(ctx).Errorf("begin transaction error: %v", tx.Error)
		return nil, tx.Error
	}
	defer func() {
		if rec := recover(); rec != nil {
			tx.Rollback()
			panic(rec)
		}
	}()

	if err := tx.WithContext(ctx).Create(audiobook).Error; err != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("create audiobook error: %v", err)
		if logErr := r.data.LogError(ctx, "audiobook", err.Error(), "tx.Create", *audiobook); logErr != nil {
			r.log.WithContext(ctx).Errorf("audiobookRepo: kafka send errorlog error: %v", logErr)
		}
		return nil, err
	}

	if err := tx.Commit().Error; err != nil {
		r.log.WithContext(ctx).Errorf("audiobook: commit transaction error: %v", err)
		if logErr := r.data.LogError(ctx, "audiobook", err.Error(), "tx.Commit", time.Now()); logErr != nil {
			r.log.WithContext(ctx).Errorf("audiobookRepo: kafka send errorlog error: %v", logErr)
		}
		return nil, err
	}

	// 发送SQL日志到Kafka
	sql := `INSERT INTO audiobooks (bid, view, author, name, playcount, chapternum, rating, description) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	params := []any{audiobook.BID, audiobook.View, audiobook.Author, audiobook.Name, audiobook.Playcount, audiobook.Chapternum, audiobook.Rating, audiobook.Description}
	if logErr := r.data.LogSQLAudit(ctx, "audiobook", sql, params); logErr != nil {
		r.log.WithContext(ctx).Errorf("audiobookRepo: kafka send sqllog error: %v", logErr)
	}

	// 事务提交后再删除缓存；缓存删除失败仅记录日志，不回滚已提交事务
	if r.data.redis != nil {
		cacheKey := "audiobook:list"
		if err := r.data.redis.Del(ctx, cacheKey).Err(); err != nil {
			r.log.WithContext(ctx).Warnf("audiobookRepo: delete cache error after commit: %v", err)
			if logErr := r.data.LogError(ctx, "audiobook", err.Error(), "redis.Del", cacheKey); logErr != nil {
				r.log.WithContext(ctx).Errorf("audiobookRepo: kafka send errorlog error: %v", logErr)
			}
		}
	}

	return &biz.Audiobook{
		BID:         audiobook.BID,
		View:        audiobook.View,
		Author:      audiobook.Author,
		Name:        audiobook.Name,
		Playcount:   audiobook.Playcount,
		Chapternum:  audiobook.Chapternum,
		Rating:      audiobook.Rating,
		Description: audiobook.Description,
		CreatedAt:   audiobook.CreatedAt,
		UpdatedAt:   audiobook.UpdatedAt,
	}, nil
}

// CreateAudiobookDetail 创建有声书章节
func (r *audiobookRepo) CreateAudiobookDetail(ctx context.Context, d *biz.AudiobookDetail) (*biz.AudiobookDetail, error) {
	// 开启事务
	tx := r.data.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		r.log.WithContext(ctx).Errorf("audiobookRepo: begin transaction error: %v", tx.Error)
		return nil, tx.Error
	}
	defer func() {
		if rec := recover(); rec != nil {
			tx.Rollback()
			panic(rec)
		}
	}()

	detail := &AudiobookDetail{
		BID:      d.BID,
		Chapter:  d.Chapter,
		Audio:    d.Audio,
		Name:     d.Name,
		Duration: d.Duration,
	}

	// 创建章节记录
	if err := tx.WithContext(ctx).Create(detail).Error; err != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("audiobook: create audiobook detail error: %v", err)
		if logErr := r.data.LogError(ctx, "audiobook", err.Error(), "tx.Create", detail); logErr != nil {
			r.log.WithContext(ctx).Errorf("audiobookRepo: kafka send errorlog error: %v", logErr)
		}
		return nil, err
	}

	// 更新有声书章节数，并校验目标有声书存在以防产生孤儿章节
	res := tx.WithContext(ctx).Model(&Audiobook{}).Where("bid = ?", detail.BID).UpdateColumn("chapternum", gorm.Expr("chapternum + ?", 1))
	if res.Error != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("audiobookRepo: update audiobook chapternum error: %v", res.Error)
		if logErr := r.data.LogError(ctx, "audiobook", res.Error.Error(), ".Where('bid = ?', detail.BID).UpdateColumn('chapternum', gorm.Expr('chapternum + ?', 1)", detail.BID); logErr != nil {
			r.log.WithContext(ctx).Errorf("audiobookRepo: kafka send errorlog error: %v", logErr)
		}
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		tx.Rollback()
		r.log.WithContext(ctx).Warnf("audiobookRepo: audiobook not found for detail: BID=%s", detail.BID)
		return nil, biz.ErrAudiobookNotFound
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		r.log.WithContext(ctx).Errorf("audiobookRepo: commit transaction error: %v", err)
		if logErr := r.data.LogError(ctx, "audiobook", err.Error(), "tx.Commit", time.Now()); logErr != nil {
			r.log.WithContext(ctx).Errorf("audiobookRepo: kafka send errorlog error: %v", logErr)
		}
		return nil, err
	}

	// 发送SQL日志到Kafka
	sql1 := `INSERT INTO audiobook_details (did, bid, chapter, audio, name, duration) VALUES ($1, $2, $3, $4, $5, $6)`
	params1 := []any{detail.DID, detail.BID, detail.Chapter, detail.Audio, detail.Name, detail.Duration}
	if logErr := r.data.LogSQLAudit(ctx, "audiobook", sql1, params1); logErr != nil {
		r.log.WithContext(ctx).Errorf("audiobookRepo: kafka send sqllog error: %v", logErr)
	}

	sql2 := `UPDATE audiobooks SET chapternum = chapternum + 1 WHERE bid = $1`
	params2 := []any{detail.BID}
	if logErr := r.data.LogSQLAudit(ctx, "audiobook", sql2, params2); logErr != nil {
		r.log.WithContext(ctx).Errorf("audiobookRepo: kafka send sqllog error: %v", logErr)
	}

	// 事务提交后再删除相关缓存；缓存删除失败仅记录日志，不回滚已提交事务
	if r.data.redis != nil {
		pipeline := r.data.redis.Pipeline()
		pipeline.Del(ctx, "audiobook:list")
		pipeline.Del(ctx, fmt.Sprintf("audiobook:details:%s", detail.BID))
		if _, err := pipeline.Exec(ctx); err != nil {
			r.log.WithContext(ctx).Warnf("audiobookRepo: delete cache error after commit: %v", err)
			if logErr := r.data.LogError(ctx, "audiobook", err.Error(), "pipeline.Del", time.Now()); logErr != nil {
				r.log.WithContext(ctx).Errorf("audiobookRepo: kafka send errorlog error: %v", logErr)
			}
		}
	}

	return &biz.AudiobookDetail{
		DID:       detail.DID,
		BID:       detail.BID,
		Chapter:   detail.Chapter,
		Audio:     detail.Audio,
		Name:      detail.Name,
		Duration:  detail.Duration,
		CreatedAt: detail.CreatedAt,
		UpdatedAt: detail.UpdatedAt,
	}, nil
}

// GetAudiobooks 获取所有有声书（Cache-Aside + singleflight，按评分和创建时间倒序）
func (r *audiobookRepo) GetAudiobooks(ctx context.Context) ([]*biz.Audiobook, error) {
	// 1. 尝试从缓存获取
	if r.data.redis != nil {
		cacheData, err := r.data.redis.Get(ctx, audiobookListCacheKey).Result()
		if err == nil {
			var audiobooks []*Audiobook
			if unmarshalErr := json.Unmarshal([]byte(cacheData), &audiobooks); unmarshalErr == nil {
				return convertToBizAudiobooks(audiobooks), nil
			}
		}
	}

	// 2. 缓存未命中：singleflight 合并回源请求
	val, err, _ := r.sf.Do(audiobookListCacheKey, func() (interface{}, error) {
		sfCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		var audiobooks []*Audiobook
		if err := r.data.db.WithContext(sfCtx).Order("rating DESC, created_at DESC").Find(&audiobooks).Error; err != nil {
			r.log.WithContext(ctx).Errorf("audiobookRepo: get audiobooks error: %v", err)
			if logErr := r.data.LogError(sfCtx, "audiobook", err.Error(), "db.Order('rating DESC, created_at DESC').Find(&audiobooks)", nil); logErr != nil {
				r.log.WithContext(ctx).Errorf("audiobookRepo: log error failed: %v", logErr)
			}
			return nil, err
		}

		if r.data.redis != nil {
			if cacheData, marshalErr := json.Marshal(audiobooks); marshalErr == nil {
				if setErr := r.data.redis.Set(sfCtx, audiobookListCacheKey, string(cacheData), cacheTTL(24*time.Hour)).Err(); setErr != nil {
					r.log.WithContext(ctx).Errorf("audiobookRepo: set cache error: %v", setErr)
				}
			}
		}

		return audiobooks, nil
	})
	if err != nil {
		return nil, err
	}

	audiobooks, _ := val.([]*Audiobook)
	return convertToBizAudiobooks(audiobooks), nil
}

// GetAudiobookDetails 获取指定有声书的所有章节（缓存优先 + singleflight + 短 TTL 负缓存）
func (r *audiobookRepo) GetAudiobookDetails(ctx context.Context, bid string) ([]*biz.AudiobookDetail, error) {
	cacheKey := audiobookDetailsKeyPrefix + bid

	// 1. 先查 Redis 缓存（包含短 TTL 空值负缓存）
	if r.data.redis != nil {
		cacheData, err := r.data.redis.Get(ctx, cacheKey).Result()
		if err == nil {
			if cacheData == audiobookNotFoundSentinel {
				return nil, biz.ErrAudiobookNotFound
			}
			var details []*AudiobookDetail
			if unmarshalErr := json.Unmarshal([]byte(cacheData), &details); unmarshalErr == nil {
				return convertToBizAudiobookDetails(details), nil
			}
		}
	}

	// 2. 缓存未命中：使用 singleflight 回源查询数据库
	val, err, _ := r.sf.Do(cacheKey, func() (interface{}, error) {
		sfCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		// 先校验有声书是否存在，区分 404 与 500
		var audiobook Audiobook
		if err := r.data.db.WithContext(sfCtx).Where("bid = ?", bid).First(&audiobook).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if r.data.redis != nil {
					if setErr := r.data.redis.Set(sfCtx, cacheKey, audiobookNotFoundSentinel, audiobookNotFoundTTL).Err(); setErr != nil {
						r.log.WithContext(ctx).Warnf("audiobookRepo: set negative sentinel error for %s: %v", bid, setErr)
					}
				}
				return nil, biz.ErrAudiobookNotFound
			}
			r.log.WithContext(ctx).Errorf("audiobookRepo: check audiobook existence error: %v", err)
			return nil, err
		}

		var details []*AudiobookDetail
		if err := r.data.db.WithContext(sfCtx).Where("bid = ?", bid).Order("chapter asc").Find(&details).Error; err != nil {
			r.log.WithContext(ctx).Errorf("audiobookRepo: get audiobook details error: %v", err)
			if logErr := r.data.LogError(sfCtx, "audiobook", err.Error(), "db.Where('bid = ?', bid).Order('chapter asc').Find(&details)", bid); logErr != nil {
				r.log.WithContext(ctx).Errorf("audiobookRepo: log error failed: %v", logErr)
			}
			return nil, err
		}

		if r.data.redis != nil {
			if cacheData, marshalErr := json.Marshal(details); marshalErr == nil {
				if setErr := r.data.redis.Set(sfCtx, cacheKey, string(cacheData), cacheTTL(48*time.Hour)).Err(); setErr != nil {
					r.log.WithContext(ctx).Errorf("audiobookRepo: set cache error: %v", setErr)
					if logErr := r.data.LogError(sfCtx, "audiobook", setErr.Error(), "redis.Set", cacheKey); logErr != nil {
						r.log.WithContext(ctx).Errorf("audiobookRepo: log error failed: %v", logErr)
					}
				}
			}
		}

		return details, nil
	})
	if err != nil {
		return nil, err
	}

	details, _ := val.([]*AudiobookDetail)
	return convertToBizAudiobookDetails(details), nil
}

// 转换为业务层有声书列表
func convertToBizAudiobooks(audiobooks []*Audiobook) []*biz.Audiobook {
	result := make([]*biz.Audiobook, 0, len(audiobooks))
	for _, a := range audiobooks {
		result = append(result, &biz.Audiobook{
			BID:         a.BID,
			View:        a.View,
			Author:      a.Author,
			Name:        a.Name,
			Playcount:   a.Playcount,
			Chapternum:  a.Chapternum,
			Rating:      a.Rating,
			Description: a.Description,
			CreatedAt:   a.CreatedAt,
			UpdatedAt:   a.UpdatedAt,
		})
	}
	return result
}

// 转换为业务层有声书章节列表
func convertToBizAudiobookDetails(details []*AudiobookDetail) []*biz.AudiobookDetail {
	result := make([]*biz.AudiobookDetail, 0, len(details))
	for _, d := range details {
		result = append(result, &biz.AudiobookDetail{
			DID:       d.DID,
			BID:       d.BID,
			Chapter:   d.Chapter,
			Audio:     d.Audio,
			Name:      d.Name,
			Duration:  d.Duration,
			CreatedAt: d.CreatedAt,
			UpdatedAt: d.UpdatedAt,
		})
	}
	return result
}

// cacheTTL 给固定 TTL 加 0-10 分钟随机抖动，避免同一时刻集中过期。
func cacheTTL(base time.Duration) time.Duration {
	return base + time.Duration(rand.Intn(600))*time.Second
}
