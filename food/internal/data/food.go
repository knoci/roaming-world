package data

import (
	"context"
	"math/rand"
	"time"

	"github.com/knoci/roaming-world/food/internal/biz"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Food 数据库模型定义	ype Food struct {
	FID       string    `gorm:"primaryKey;type:varchar(36);column:fid" json:"fid"`
	View      []string  `gorm:"type:text;not null;serializer:json" json:"view"`
	Name      string    `gorm:"type:varchar(50);not null" json:"name"`
	Describe  string    `gorm:"type:text" json:"describe"`
	Recipe    string    `gorm:"type:text" json:"recipe"`
	Article   string    `gorm:"type:text" json:"article"`
	Location  string    `gorm:"type:varchar(30);not null" json:"location"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定 Food 模型对应的数据库表名
func (Food) TableName() string {
	return "foods"
}

// BeforeCreate 在创建记录前生成 FID
func (f *Food) BeforeCreate(tx *gorm.DB) error {
	if f.FID == "" {
		f.FID = uuid.New().String()
	}
	return nil
}

type foodRepo struct {
	data *Data
	log  *log.Helper
}

// NewFoodRepo . 
func NewFoodRepo(data *Data, logger log.Logger) biz.FoodRepo {
	return &foodRepo{
		data: data,
		log:  log.NewHelper(logger),
	}
}

func (r *foodRepo) CreateFood(ctx context.Context, g *biz.Food) (*biz.Food, error) {
	food := Food{
		Name:     g.Name,
		View:     g.View,
		Describe: g.Describe,
		Recipe:   g.Recipe,
		Article:  g.Article,
		Location: g.Location,
	}
	if err := r.data.db.WithContext(ctx).Create(&food).Error; err != nil {
		r.log.WithContext(ctx).Errorf("CreateFood error: %v", err)
		return nil, err
	}
	return &biz.Food{
		FID:       food.FID,
		View:      food.View,
		Name:      food.Name,
		Describe:  food.Describe,
		Recipe:    food.Recipe,
		Article:   food.Article,
		Location:  food.Location,
		CreatedAt: food.CreatedAt,
		UpdatedAt: food.UpdatedAt,
	}, nil
}

func (r *foodRepo) GetFoodList(ctx context.Context) ([]*biz.Food, error) {
	var foods []Food
	if err := r.data.db.WithContext(ctx).Find(&foods).Error; err != nil {
		r.log.WithContext(ctx).Errorf("GetFoodList error: %v", err)
		return nil, err
	}

	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(foods), func(i, j int) { foods[i], foods[j] = foods[j], foods[i] })

	var bizFoods []*biz.Food
	for _, food := range foods {
		bizFoods = append(bizFoods, &biz.Food{
			FID:       food.FID,
			View:      food.View,
			Name:      food.Name,
			Describe:  food.Describe,
			Recipe:    food.Recipe,
			Article:   food.Article,
			Location:  food.Location,
			CreatedAt: food.CreatedAt,
			UpdatedAt: food.UpdatedAt,
		})
	}
	return bizFoods, nil
}

func (r *foodRepo) GetRandomFood(ctx context.Context) (*biz.Food, error) {
	var foods []Food
	if err := r.data.db.WithContext(ctx).Find(&foods).Error; err != nil {
		r.log.WithContext(ctx).Errorf("GetRandomFood - find error: %v", err)
		return nil, err
	}

	if len(foods) == 0 {
		r.log.WithContext(ctx).Info("GetRandomFood - no food found")
		return nil, nil // 或者返回一个特定的错误，如 ErrFoodNotFound
	}

	rand.Seed(time.Now().UnixNano())
	randomFood := foods[rand.Intn(len(foods))]

	return &biz.Food{
		FID:       randomFood.FID,
		View:      randomFood.View,
		Name:      randomFood.Name,
		Describe:  randomFood.Describe,
		Recipe:    randomFood.Recipe,
		Article:   randomFood.Article,
		Location:  randomFood.Location,
		CreatedAt: randomFood.CreatedAt,
		UpdatedAt: randomFood.UpdatedAt,
	}, nil
}