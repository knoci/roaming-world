package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"travel-world/initialize"
	"travel-world/model"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type SceneService struct {
	db     *gorm.DB
	logger *zap.Logger
}

func NewSceneService() *SceneService {
	return &SceneService{
		db:     initialize.DB,
		logger: initialize.Logger,
	}
}

type SceneRequest struct {
	Name     string   `json:"name" binding:"required,min=2,max=100"`
	Describe string   `json:"describe" binding:"required,max=100"`
	View     []string `json:"view" binding:"required,dive,max=200"`
	Location string   `json:"location" binding:"required,max=20"`
	Article  string   `json:"article" binding:"required"`
}

func (s *SceneService) CreateScene(ctx context.Context, req *SceneRequest) (*model.Scene, error) {
	scene := &model.Scene{
		Name:     req.Name,
		Describe: req.Describe,
		View:     req.View,
		Location: req.Location,
		Article:  req.Article,
	}

	err := s.db.Create(scene).Error
	if err != nil {
		s.logger.Error("create scene failed", zap.Error(err))
		return nil, err
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("INSERT INTO scenes (sid, name, describe, view, location, article) VALUES ('%s', '%s', '%s', '%s', '%s', '%s')",
		scene.SID, scene.Name, scene.Describe, scene.View, scene.Location, scene.Article); err != nil {
		s.logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	return scene, nil
}

func (s *SceneService) DeleteScene(ctx context.Context, sid string) error {
	err := s.db.Delete(&model.Scene{}, "sid = ?", sid).Error
	if err != nil {
		s.logger.Error("delete scene failed", zap.Error(err))
		return err
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("DELETE FROM scenes WHERE sid = '%s'", sid); err != nil {
		s.logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	return nil
}

func (s *SceneService) UpdateScene(ctx context.Context, scene *model.Scene) error {
	// 先查找现有的场景记录
	var existingScene model.Scene
	if err := s.db.Where("sid = ?", scene.SID).First(&existingScene).Error; err != nil {
		s.logger.Error("find scene failed", zap.Error(err))
		return err
	}

	// 只更新提供的字段
	updates := map[string]interface{}{}
	if scene.Name != "" {
		updates["name"] = scene.Name
	}
	if scene.Describe != "" {
		updates["describe"] = scene.Describe
	}
	if len(scene.View) > 0 {
		viewJSON, err := json.Marshal(scene.View)
		if err != nil {
			s.logger.Error("marshal view failed", zap.Error(err))
			return err
		}
		updates["view"] = string(viewJSON)
	}
	if scene.Location != "" {
		updates["location"] = scene.Location
	}
	if scene.Article != "" {
		updates["article"] = scene.Article
	}

	// 更新记录
	err := s.db.Model(&existingScene).Updates(updates).Error
	if err != nil {
		s.logger.Error("update scene failed", zap.Error(err))
		return err
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("UPDATE scenes SET name = '%s', describe = '%s', view = '%s', location = '%s', article = '%s' WHERE sid = '%s'",
		existingScene.Name, existingScene.Describe, existingScene.View, existingScene.Location, existingScene.Article, existingScene.SID); err != nil {
		s.logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	return nil
}

func (s *SceneService) SearchScene(ctx context.Context, keyword string) ([]*model.Scene, error) {
	var scenes []*model.Scene
	var allScenes []*model.Scene
	seenSIDs := make(map[string]bool)

	// 按空格分割关键词
	keywords := strings.Fields(keyword)

	// 如果没有关键词，返回错误
	if len(keywords) == 0 {
		return nil, fmt.Errorf("关键词不能为空")
	}

	// 对每个关键词进行搜索
	for _, kw := range keywords {
		err := s.db.Where("name LIKE ?", fmt.Sprintf("%%%s%%", kw)).Find(&scenes).Error
		if err != nil {
			s.logger.Error("search scene failed", zap.Error(err))
			return nil, err
		}

		// 去重并添加到结果集
		for _, scene := range scenes {
			if !seenSIDs[scene.SID] {
				seenSIDs[scene.SID] = true
				allScenes = append(allScenes, scene)
			}
		}
	}

	return allScenes, nil
}

func (s *SceneService) ListScenes(ctx context.Context, limit int) ([]*model.Scene, error) {
	var scenes []*model.Scene
	db := s.db
	if limit > 0 {
		db = db.Limit(limit)
	}
	err := db.Find(&scenes).Error
	if err != nil {
		s.logger.Error("list scenes failed", zap.Error(err))
		return nil, err
	}
	return scenes, nil
}

func (s *SceneService) GetSceneByID(ctx context.Context, sid string) (*model.Scene, error) {
	var scene model.Scene
	err := s.db.Where("sid = ?", sid).First(&scene).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		s.logger.Error("get scene by id failed", zap.Error(err))
		return nil, err
	}
	return &scene, nil
}
