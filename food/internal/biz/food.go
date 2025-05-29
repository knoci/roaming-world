package biz

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	v1 "github.com/knoci/roaming-world/food/api/food/v1"
)

// Food 结构定义了食物的属性
type Food struct {
	FID       string
	View      []string
	Name      string
	Describe  string
	Recipe    string
	Article   string
	Location  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FoodRepo 定义了 Food 数据仓库的接口
type FoodRepo interface {
	CreateFood(ctx context.Context, food *Food) (*Food, error)
	GetFoodList(ctx context.Context) ([]*Food, error)
	GetRandomFood(ctx context.Context) (*Food, error)
}

// FoodUsecase 定义了 Food 相关的业务逻辑
type FoodUsecase struct {
	repo FoodRepo
	log  *log.Helper
}

// NewFoodUsecase 创建一个新的 FoodUsecase
func NewFoodUsecase(repo FoodRepo, logger log.Logger) *FoodUsecase {
	return &FoodUsecase{repo: repo, log: log.NewHelper(logger)}
}

// CreateFood 创建食物的业务逻辑
func (uc *FoodUsecase) CreateFood(ctx context.Context, req *v1.CreateFoodRequest) (*Food, error) {
	uc.log.WithContext(ctx).Infof("foodUsecase: CreateFood: %v", req.Name)
	food := &Food{
		Name:     req.Name,
		View:     req.View,
		Describe: req.Describe,
		Recipe:   req.Recipe,
		Article:  req.Article,
		Location: req.Location,
	}
	food, err := uc.repo.CreateFood(ctx, food)
	if err != nil  {
		return nil, ErrInternalError
	}
	return food, nil
}

// GetFoodList 获取食物列表的业务逻辑
func (uc *FoodUsecase) GetFoodList(ctx context.Context) ([]*Food, error) {
	uc.log.WithContext(ctx).Info("foodUsecase: GetFoodList")
	foods, err := uc.repo.GetFoodList(ctx)
	if err != nil  {
		return nil, ErrInternalError
	}
	return foods, nil
}

// GetRandomFood 获取随机食物的业务逻辑
func (uc *FoodUsecase) GetRandomFood(ctx context.Context) (*Food, error) {
	uc.log.WithContext(ctx).Info("foodUsecase: GetRandomFood")
	food, err := uc.repo.GetRandomFood(ctx)
	if err != nil  {
		return nil, ErrInternalError
	}
	return food, nil
}
