package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"travel-world/initialize"
	"travel-world/model"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type AudiobookService struct{}

// CreateAudiobook 创建有声书
func (s *AudiobookService) CreateAudiobook(audiobook *model.Audiobook) error {
	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 创建有声书记录
	if err := tx.Create(audiobook).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("创建有声书失败: %v", err)
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("INSERT INTO audiobooks (bid, view, author, name, playcount, chapternum, rating, description) VALUES ('%s', '%s', '%s', '%s', %d, %d, %f, '%s')",
		audiobook.BID, audiobook.View, audiobook.Author, audiobook.Name, audiobook.Playcount, audiobook.Chapternum, audiobook.Rating, audiobook.Description); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 删除缓存
	ctx := context.Background()
	cacheKey := "audiobooks_list"
	if err := initialize.RDB.Del(ctx, cacheKey).Err(); err != nil {
		tx.Rollback()
		return fmt.Errorf("删除缓存失败: %v", err)
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("提交事务失败: %v", err)
	}

	return nil
}

// CreateAudiobookDetail 创建有声书章节
func (s *AudiobookService) CreateAudiobookDetail(detail *model.AudiobookDetail) error {
	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 创建章节记录
	if err := tx.Create(detail).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("创建章节失败: %v", err)
	}

	// 更新有声书章节数
	if err := tx.Model(&model.Audiobook{}).Where("bid = ?", detail.BID).UpdateColumn("chapternum", gorm.Expr("chapternum + ?", 1)).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("更新章节数失败: %v", err)
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("INSERT INTO audiobook_details (did, bid, chapter, audio, name, duration) VALUES ('%s', '%s', '%d', '%s', '%s', %d)",
		detail.DID, detail.BID, detail.Chapter, detail.Audio, detail.Name, detail.Duration); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 发送更新章节数的SQL日志到Kafka
	if err := initialize.SendSQLLog("UPDATE audiobooks SET chapternum = chapternum + 1 WHERE bid = '%s'", detail.BID); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 删除相关缓存
	ctx := context.Background()
	pipeline := initialize.RDB.Pipeline()

	// 删除有声书列表缓存
	pipeline.Del(ctx, "audiobooks_list")
	// 删除该有声书的章节列表缓存
	pipeline.Del(ctx, fmt.Sprintf("audiobook_details:%s", detail.BID))

	_, err := pipeline.Exec(ctx)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("删除缓存失败: %v", err)
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("提交事务失败: %v", err)
	}

	return nil
}

// GetAudiobooks 获取所有有声书
func (s *AudiobookService) GetAudiobooks() ([]model.Audiobook, error) {
	var audiobooks []model.Audiobook
	ctx := context.Background()

	// 尝试从缓存获取
	cacheKey := "audiobooks_list"
	cacheData, err := initialize.RDB.Get(ctx, cacheKey).Result()
	if err == nil {
		if err := json.Unmarshal([]byte(cacheData), &audiobooks); err == nil {
			return audiobooks, nil
		}
	}

	// 从数据库获取
	if err := initialize.DB.Order("rating asc").Find(&audiobooks).Error; err != nil {
		return nil, fmt.Errorf("获取有声书列表失败: %v", err)
	}

	// 更新缓存
	if cacheData, err := json.Marshal(audiobooks); err == nil {
		initialize.RDB.Set(ctx, cacheKey, string(cacheData), time.Hour*24)
	}

	return audiobooks, nil
}

// RebuildAudiobookCache 重建有声书缓存
func (s *AudiobookService) RebuildAudiobookCache() error {
	ctx := context.Background()
	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取所有有声书
	var audiobooks []model.Audiobook
	if err := tx.Order("rating asc").Find(&audiobooks).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("获取有声书列表失败: %v", err)
	}

	// 使用Pipeline批量更新缓存
	pipeline := initialize.RDB.Pipeline()

	// 更新有声书列表缓存
	if cacheData, err := json.Marshal(audiobooks); err == nil {
		pipeline.Set(ctx, "audiobooks_list", string(cacheData), time.Hour*24)
	}

	// 获取并更新每个有声书的章节缓存
	for _, book := range audiobooks {
		var details []model.AudiobookDetail
		if err := tx.Where("bid = ?", book.BID).Order("chapter asc").Find(&details).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("获取章节列表失败: %v", err)
		}

		if cacheData, err := json.Marshal(details); err == nil {
			pipeline.Set(ctx, fmt.Sprintf("audiobook_details:%s", book.BID), string(cacheData), time.Hour*48)
		}
	}

	// 执行Pipeline
	_, err := pipeline.Exec(ctx)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("更新缓存失败: %v", err)
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("提交事务失败: %v", err)
	}

	initialize.Logger.Info("Successfully rebuilt audiobook cache")
	return nil
}

// GetAudiobookDetails 获取指定有声书的所有章节
func (s *AudiobookService) GetAudiobookDetails(bid string) ([]model.AudiobookDetail, error) {
	// 先检查有声书是否存在
	var audiobook model.Audiobook
	if err := initialize.DB.Where("bid = ?", bid).First(&audiobook).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("有声书不存在")
		}
		return nil, fmt.Errorf("查询有声书失败: %v", err)
	}

	var details []model.AudiobookDetail
	ctx := context.Background()

	// 尝试从缓存获取
	cacheKey := fmt.Sprintf("audiobook_details:%s", bid)
	cacheData, err := initialize.RDB.Get(ctx, cacheKey).Result()
	if err == nil {
		if err := json.Unmarshal([]byte(cacheData), &details); err == nil {
			return details, nil
		}
	}

	// 从数据库获取
	if err := initialize.DB.Where("bid = ?", bid).Order("chapter asc").Find(&details).Error; err != nil {
		return nil, fmt.Errorf("获取章节列表失败: %v", err)
	}

	// 如果没有找到任何记录，返回空切片
	if len(details) == 0 {
		details = make([]model.AudiobookDetail, 0)
	} else {
		// 更新缓存
		if cacheData, err := json.Marshal(details); err == nil {
			initialize.RDB.Set(ctx, cacheKey, string(cacheData), time.Hour*24)
		}
	}

	return details, nil
}
