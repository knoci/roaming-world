package biz

import (
	"context"
	"time"

	v1 "github.com/knoci/roaming-world/food/api/food/v1"
	"github.com/go-kratos/kratos/v2/log"
)

// Food 结构定义了食物的属性	ype Food struct {
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

// FoodRepo 定义了 Food 数据仓库的接口	ype FoodRepo interface {
	CreateFood(ctx context.Context, food *Food) (*Food, error)
	GetFoodList(ctx context.Context) ([]*Food, error)
	GetRandomFood(ctx context.Context) (*Food, error)
}

// FoodUsecase 定义了 Food 相关的业务逻辑	ype FoodUsecase struct {
	repo FoodRepo
	log  *log.Helper
}

// NewFoodUsecase 创建一个新的 FoodUsecase
func NewFoodUsecase(repo FoodRepo, logger log.Logger) *FoodUsecase {
	return &FoodUsecase{repo: repo, log: log.NewHelper(logger)}
}

// CreateFood 创建食物的业务逻辑
func (uc *FoodUsecase) CreateFood(ctx context.Context, req *v1.CreateFoodRequest) (*Food, error) {
	uc.log.WithContext(ctx).Infof("CreateFood: %v", req.Name)
	food := &Food{
		Name:     req.Name,
		View:     req.View,
		Describe: req.Describe,
		Recipe:   req.Recipe,
		Article:  req.Article,
		Location: req.Location,
	}
	return uc.repo.CreateFood(ctx, food)
}

// GetFoodList 获取食物列表的业务逻辑
func (uc *FoodUsecase) GetFoodList(ctx context.Context) ([]*Food, error) {
	uc.log.WithContext(ctx).Info("GetFoodList")
	return uc.repo.GetFoodList(ctx)
}

// GetRandomFood 获取随机食物的业务逻辑
func (uc *FoodUsecase) GetRandomFood(ctx context.Context) (*Food, error) {
	uc.log.WithContext(ctx).Info("GetRandomFood")
	return uc.repo.GetRandomFood(ctx)
}