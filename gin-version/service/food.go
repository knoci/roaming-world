package service

import (
	"math/rand"
	"time"
	"travel-world/initialize"
	"travel-world/model"

	"go.uber.org/zap"
)

type FoodService struct{}

type CreateFoodRequest struct {
	Name     string   `json:"name" binding:"required"`
	View     []string `json:"view" binding:"required,dive,max=200"`
	Describe string   `json:"describe" binding:"required"`
	Recipe   string   `json:"recipe" binding:"required"`
	Article  string   `json:"article" binding:"required"`
	Location string   `json:"location" binding:"required"`
}

// CreateFood 创建食物
func (s *FoodService) CreateFood(req *CreateFoodRequest) (*model.Food, error) {
	food := &model.Food{
		Name:     req.Name,
		Describe: req.Describe,
		Article:  req.Article,
		Location: req.Location,
		Recipe:   req.Recipe,
		View:     req.View,
	}

	// 保存到数据库
	if err := initialize.DB.Create(food).Error; err != nil {
		return nil, err
	}

	// 记录操作日志到Kafka
	if err := initialize.SendSQLLog("INSERT INTO foods (fid, name, view, describe, article, location, recipe) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s')",
		food.FID, food.Name, food.View, food.Describe, food.Article, food.Location, food.Recipe); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	return food, nil
}

func NewFoodService() *FoodService {
	return &FoodService{}
}

// GetFoodList 获取随机排序的食物列表
func (s *FoodService) GetFoodList() ([]model.Food, error) {
	var foods []model.Food
	result := initialize.DB.Find(&foods)
	if result.Error != nil {
		return nil, result.Error
	}

	// 使用当前时间作为随机种子
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	// 随机打乱食物列表顺序
	r.Shuffle(len(foods), func(i, j int) {
		foods[i], foods[j] = foods[j], foods[i]
	})

	return foods, nil
}

// GetRandomFood 获取随机单个食物
func (s *FoodService) GetRandomFood() (*model.Food, error) {
	var foods []model.Food
	result := initialize.DB.Find(&foods)
	if result.Error != nil {
		return nil, result.Error
	}

	if len(foods) == 0 {
		return nil, nil
	}

	// 使用当前时间作为随机种子
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	// 随机选择一个食物
	randomFood := foods[r.Intn(len(foods))]

	return &randomFood, nil
}
