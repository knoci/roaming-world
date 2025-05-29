package data

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"scene/internal/biz"
)

// Scene 场景模型
type Scene struct {
	SID       string    `gorm:"primaryKey;type:varchar(36);column:sid" json:"sid"`
	Name      string    `gorm:"type:varchar(100);not null" json:"name"`
	Describe  string    `gorm:"type:varchar(100);not null" json:"describe"`
	View      []string  `gorm:"type:text;not null;serializer:json" json:"view"`
	Location  string    `gorm:"type:varchar(30);not null" json:"location"`
	Article   string    `gorm:"type:text;not null" json:"article"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Scene) BeforeCreate(tx *gorm.DB) error {
	if s.SID == "" {
		s.SID = uuid.New().String()
	}
	return nil
}

type sceneRepo struct {
	data *Data
	log  *log.Helper
}

// NewSceneRepo 创建场景仓库实例
func NewSceneRepo(data *Data, logger log.Logger) biz.SceneRepo {
	return &sceneRepo{
		data: data,
		log:  log.NewHelper(logger),
	}
}

// CreateScene 创建场景
func (r *sceneRepo) CreateScene(ctx context.Context, s *biz.Scene) (*biz.Scene, error) {
	scene := &Scene{
		Name:     s.Name,
		Describe: s.Describe,
		View:     s.View,
		Location: s.Location,
		Article:  s.Article,
	}

	result := r.data.db.Create(scene)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("create scene error: %v", result.Error)
		return nil, result.Error
	}

	// 发送SQL日志到Kafka
	viewJSON, _ := json.Marshal(scene.View)
	sql := fmt.Sprintf("INSERT INTO scenes (sid, name, describe, view, location, article) VALUES ('%s', '%s', '%s', '%s', '%s', '%s')",
		scene.SID, scene.Name, scene.Describe, string(viewJSON), scene.Location, scene.Article)
	if err := r.sendSQLLog(ctx, sql); err != nil {
		r.log.WithContext(ctx).Errorf("send SQL log error: %v", err)
	}

	return &biz.Scene{
		SID:       scene.SID,
		Name:      scene.Name,
		Describe:  scene.Describe,
		View:      scene.View,
		Location:  scene.Location,
		Article:   scene.Article,
		CreatedAt: scene.CreatedAt,
		UpdatedAt: scene.UpdatedAt,
	}, nil
}

// DeleteScene 删除场景
func (r *sceneRepo) DeleteScene(ctx context.Context, sid string) error {
	result := r.data.db.Delete(&Scene{}, "sid = ?", sid)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("delete scene error: %v", result.Error)
		return result.Error
	}

	// 发送SQL日志到Kafka
	sql := fmt.Sprintf("DELETE FROM scenes WHERE sid = '%s'", sid)
	if err := r.sendSQLLog(ctx, sql); err != nil {
		r.log.WithContext(ctx).Errorf("send SQL log error: %v", err)
	}

	return nil
}

// UpdateScene 更新场景
func (r *sceneRepo) UpdateScene(ctx context.Context, s *biz.Scene) (*biz.Scene, error) {
	// 先查找现有的场景记录
	var existingScene Scene
	if err := r.data.db.Where("sid = ?", s.SID).First(&existingScene).Error; err != nil {
		r.log.WithContext(ctx).Errorf("find scene error: %v", err)
		return nil, err
	}

	// 只更新提供的字段
	updates := map[string]interface{}{}
	if s.Name != "" {
		updates["name"] = s.Name
		existingScene.Name = s.Name
	}
	if s.Describe != "" {
		updates["describe"] = s.Describe
		existingScene.Describe = s.Describe
	}
	if len(s.View) > 0 {
		updates["view"] = s.View
		existingScene.View = s.View
	}
	if s.Location != "" {
		updates["location"] = s.Location
		existingScene.Location = s.Location
	}
	if s.Article != "" {
		updates["article"] = s.Article
		existingScene.Article = s.Article
	}

	// 更新记录
	result := r.data.db.Model(&existingScene).Updates(updates)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("update scene error: %v", result.Error)
		return nil, result.Error
	}

	// 发送SQL日志到Kafka
	viewJSON, _ := json.Marshal(existingScene.View)
	sql := fmt.Sprintf("UPDATE scenes SET name = '%s', describe = '%s', view = '%s', location = '%s', article = '%s' WHERE sid = '%s'",
		existingScene.Name, existingScene.Describe, string(viewJSON), existingScene.Location, existingScene.Article, existingScene.SID)
	if err := r.sendSQLLog(ctx, sql); err != nil {
		r.log.WithContext(ctx).Errorf("send SQL log error: %v", err)
	}

	return &biz.Scene{
		SID:       existingScene.SID,
		Name:      existingScene.Name,
		Describe:  existingScene.Describe,
		View:      existingScene.View,
		Location:  existingScene.Location,
		Article:   existingScene.Article,
		CreatedAt: existingScene.CreatedAt,
		UpdatedAt: existingScene.UpdatedAt,
	}, nil
}

// SearchScene 搜索场景
func (r *sceneRepo) SearchScene(ctx context.Context, keyword string) ([]*biz.Scene, error) {
	var scenes []*Scene
	var allScenes []*Scene
	seenSIDs := make(map[string]bool)

	// 按空格分割关键词
	keywords := strings.Fields(keyword)

	// 如果没有关键词，返回错误
	if len(keywords) == 0 {
		return nil, fmt.Errorf("关键词不能为空")
	}

	// 对每个关键词进行搜索
	for _, kw := range keywords {
		result := r.data.db.Where("name LIKE ?", fmt.Sprintf("%%%s%%", kw)).Find(&scenes)
		if result.Error != nil {
			r.log.WithContext(ctx).Errorf("search scene error: %v", result.Error)
			return nil, result.Error
		}

		// 去重并添加到结果集
		for _, scene := range scenes {
			if !seenSIDs[scene.SID] {
				seenSIDs[scene.SID] = true
				allScenes = append(allScenes, scene)
			}
		}
	}

	return convertToBizScenes(allScenes), nil
}

// ListScenes 获取场景列表
func (r *sceneRepo) ListScenes(ctx context.Context, limit int) ([]*biz.Scene, error) {
	var scenes []*Scene
	db := r.data.db
	if limit > 0 {
		db = db.Limit(limit)
	}
	result := db.Find(&scenes)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("list scenes error: %v", result.Error)
		return nil, result.Error
	}
	return convertToBizScenes(scenes), nil
}

// GetSceneByID 根据ID获取场景
func (r *sceneRepo) GetSceneByID(ctx context.Context, sid string) (*biz.Scene, error) {
	var scene Scene
	result := r.data.db.Where("sid = ?", sid).First(&scene)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		r.log.WithContext(ctx).Errorf("get scene by id error: %v", result.Error)
		return nil, result.Error
	}
	return &biz.Scene{
		SID:       scene.SID,
		Name:      scene.Name,
		Describe:  scene.Describe,
		View:      scene.View,
		Location:  scene.Location,
		Article:   scene.Article,
		CreatedAt: scene.CreatedAt,
		UpdatedAt: scene.UpdatedAt,
	}, nil
}

// 发送SQL日志到Kafka
func (r *sceneRepo) sendSQLLog(ctx context.Context, sql string) error {
	// 这里应该实现发送SQL日志到Kafka的逻辑
	// 由于我们没有完整的Kafka实现，这里只记录日志
	r.log.WithContext(ctx).Infof("SQL Log: %s", sql)
	return nil
}

// 转换为业务层场景列表
func convertToBizScenes(scenes []*Scene) []*biz.Scene {
	result := make([]*biz.Scene, 0, len(scenes))
	for _, s := range scenes {
		result = append(result, &biz.Scene{
			SID:       s.SID,
			Name:      s.Name,
			Describe:  s.Describe,
			View:      s.View,
			Location:  s.Location,
			Article:   s.Article,
			CreatedAt: s.CreatedAt,
			UpdatedAt: s.UpdatedAt,
		})
	}
	return result
}
