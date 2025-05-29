package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"gorm.io/gorm"

	kafka "github.com/knoci/roaming-world/audiobook/internal/pkg"
	"github.com/knoci/roaming-world/audiobook/internal/biz"
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
	BID       string    `gorm:"type:varchar(36);column:bid;index" json:"bid"`
	Chapter   int       `gorm:"default:1;not null" json:"chapter"`
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

type SqlMsg struct {
	Query  string        `json:"query"`
	Params []interface{} `json:"params"`
}

type audiobookRepo struct {
	data *Data
	log  *log.Helper
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
	tx := r.data.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Create(audiobook).Error ;err != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("create audiobook error: %v", err)
		return nil, err
	}

	// 发送SQL日志到Kafka
	sql := `INSERT INTO audiobooks (bid, view, author, name, playcount, chapternum, rating, description) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	params := []any{audiobook.BID, audiobook.View, audiobook.Author, audiobook.Name, audiobook.Playcount, audiobook.Chapternum, audiobook.Rating, audiobook.Description}
	msg := SqlMsg{
		Query:  sql,
		Params: params,
	}
	sqlbyte, err := json.Marshal(msg)
	if err != nil {
		r.log.WithContext(ctx).Errorf("kafka: json marshal error: %v", err)
	}
	log := kafka.NewMessage("audiobook", sqlbyte)
	err = r.data.kafka.Send(ctx, log)
	if err != nil {
		r.log.WithContext(ctx).Errorf("kafka send error: %v", err)
	}

	// 删除缓存
	cacheKey := "audiobooks_list"
	if err := r.data.redis.Del(ctx, cacheKey).Err(); err != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("delete cache error: %v", err)
	}

	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("audiobook: error: %v", err)
		return nil, err
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
	tx := r.data.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
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
	if err := tx.Create(detail).Error; err != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("audiobook: create audiobook detail error: %v", err)
		return nil, err
	}

	// 更新有声书章节数
	if err := tx.Model(&Audiobook{}).Where("bid = ?", detail.BID).UpdateColumn("chapternum", gorm.Expr("chapternum + ?", 1)).Error; err != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("update audiobook chapternum error: %v", err)
		return nil, err
	}

	// 发送SQL日志到Kafka
	sql1 := `INSERT INTO audiobook_details (did, bid, chapter, audio, name, duration) VALUES ($1, $2, $3, $4, $5, $6)`
	params1 := []any{detail.DID, detail.BID, detail.Chapter, detail.Audio, detail.Name, detail.Duration}
	msg1 := SqlMsg{
		Query:  sql1,
		Params: params1,
	}
	sqlbyte1, err := json.Marshal(msg1)
	if err != nil {
		r.log.WithContext(ctx).Errorf("kafka: json marshal error: %v", err)
	}
	log1 := kafka.NewMessage("audiobook", sqlbyte1)
	err = r.data.kafka.Send(ctx, log1)
	if err != nil {
		r.log.WithContext(ctx).Errorf("kafka send error: %v", err)
	}

	sql2 := `UPDATE audiobooks SET chapternum = chapternum + 1 WHERE bid = $1`
	params2 := []any{detail.BID}
	msg2 := SqlMsg{
		Query:  sql2,
		Params: params2,
	}
	sqlbyte2, err := json.Marshal(msg2)
	if err != nil {
		r.log.WithContext(ctx).Errorf("kafka: json marshal error: %v", err)
	}
	log2 := kafka.NewMessage("audiobook", sqlbyte2)
	err = r.data.kafka.Send(ctx, log2)
	if err != nil {
		r.log.WithContext(ctx).Errorf("kafka send error: %v", err)
	}

	// 删除相关缓存
	pipeline := r.data.redis.Pipeline()

	// 删除有声书列表缓存
	pipeline.Del(ctx, "audiobooks_list")
	// 删除该有声书的章节列表缓存
	pipeline.Del(ctx, fmt.Sprintf("audiobook_details:%s", detail.BID))

	_, err := pipeline.Exec(ctx)
	if err != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("delete cache error: %v", err)
		return nil, err
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		r.log.WithContext(ctx).Errorf("commit transaction error: %v", err)
		return nil, err
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

// GetAudiobooks 获取所有有声书
func (r *audiobookRepo) GetAudiobooks(ctx context.Context) ([]*biz.Audiobook, error) {
	var audiobooks []*Audiobook

	// 尝试从缓存获取
	cacheKey := "audiobooks_list"
	cacheData, err := r.data.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		if err := json.Unmarshal([]byte(cacheData), &audiobooks); err == nil {
			return convertToBizAudiobooks(audiobooks), nil
		}
	}

	// 从数据库获取
	if err := r.data.db.Order("rating asc").Find(&audiobooks).Error; err != nil {
		r.log.WithContext(ctx).Errorf("get audiobooks error: %v", err)
		return nil, err
	}

	// 更新缓存
	if cacheData, err := json.Marshal(audiobooks); err == nil {
		r.data.rdb.Set(ctx, cacheKey, string(cacheData), time.Hour*24)
	}

	return convertToBizAudiobooks(audiobooks), nil
}

// GetAudiobookDetails 获取指定有声书的所有章节
func (r *audiobookRepo) GetAudiobookDetails(ctx context.Context, bid string) ([]*biz.AudiobookDetail, error) {
	// 先检查有声书是否存在
	var audiobook Audiobook
	if err := r.data.db.Where("bid = ?", bid).First(&audiobook).Error; err != nil {
		r.log.WithContext(ctx).Errorf("audiobook not found: %v", err)
		return nil, err
	}

	var details []*AudiobookDetail

	// 尝试从缓存获取
	cacheKey := fmt.Sprintf("audiobook_details:%s", bid)
	cacheData, err := r.data.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		if err := json.Unmarshal([]byte(cacheData), &details); err == nil {
			return convertToBizAudiobookDetails(details), nil
		}
	}

	// 从数据库获取
	if err := r.data.db.Where("bid = ?", bid).Order("chapter asc").Find(&details).Error; err != nil {
		r.log.WithContext(ctx).Errorf("get audiobook details error: %v", err)
		return nil, err
	}

	// 更新缓存
	if cacheData, err := json.Marshal(details); err == nil {
		r.data.rdb.Set(ctx, cacheKey, string(cacheData), time.Hour*48)
	}

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
