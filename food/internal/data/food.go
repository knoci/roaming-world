package data

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/knoci/roaming-world/food/internal/biz"
	kafka "github.com/knoci/roaming-world/food/internal/pkg"
	"gorm.io/gorm"
)

// Food 数据库模型定义	ype Food struct {
type Food struct {
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

type SqlMsg struct {
	Query  string        `json:"query"`
	Params []interface{} `json:"params"`
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

	sql := `INSERT INTO foods (fid, name, view, describe, recipe, article, location) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	params := []any{food.FID, food.Name, food.View, food.Describe, food.Recipe, food.Article, food.Location}
	msg := SqlMsg{
		Query:  sql,
		Params: params,
	}
	sqlbyte, err := json.Marshal(msg)
	if err != nil {
		r.log.WithContext(ctx).Errorf("kafka: json marshal error: %v", err)
	}
	log := kafka.NewMessage("user", sqlbyte)
	err = r.data.kafka.Send(ctx, log)
	if err != nil {
		r.log.WithContext(ctx).Errorf("kafka send error: %v", err)
	}

	// 将食物信息保存到Redis
	// 1. 保存单个食物详情
	foodJSON, err := json.Marshal(food)
	if err != nil {
		r.log.WithContext(ctx).Errorf("Redis: json marshal food error: %v", err)
	} else {
		// 使用food:fid:{fid}作为键存储单个食物详情
		key := fmt.Sprintf("food:fid:%s", food.FID)
		err = r.data.redis.Set(ctx, key, foodJSON, 24*time.Hour).Err()
		if err != nil {
			r.log.WithContext(ctx).Errorf("Redis: set food detail error: %v", err)
		}

		// 2. 将FID添加到食物列表集合中
		err = r.data.redis.SAdd(ctx, "food:list", food.FID).Err()
		if err != nil {
			r.log.WithContext(ctx).Errorf("Redis: add to food list error: %v", err)
		}
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
	// 尝试从Redis获取食物列表
	foodIDs, err := r.data.redis.SMembers(ctx, "food:list").Result()
	if err == nil && len(foodIDs) > 0 {
		r.log.WithContext(ctx).Info("GetFoodList: using Redis cache")

		var bizFoods []*biz.Food
		for _, fid := range foodIDs {
			key := fmt.Sprintf("food:fid:%s", fid)
			foodJSON, err := r.data.redis.Get(ctx, key).Result()
			if err != nil {
				continue // 如果获取单个食物失败，跳过
			}

			var food Food
			if err := json.Unmarshal([]byte(foodJSON), &food); err != nil {
				r.log.WithContext(ctx).Errorf("Redis: unmarshal food error: %v", err)
				continue
			}

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

		// 随机打乱顺序
		rand.Seed(time.Now().UnixNano())
		rand.Shuffle(len(bizFoods), func(i, j int) { bizFoods[i], bizFoods[j] = bizFoods[j], bizFoods[i] })

		if len(bizFoods) > 0 {
			return bizFoods, nil
		}
		// 如果Redis中没有数据或获取失败，回退到数据库查询
	}

	// 从数据库获取
	var foods []Food
	if err := r.data.db.WithContext(ctx).Find(&foods).Error; err != nil {
		r.log.WithContext(ctx).Errorf("GetFoodList error: %v", err)
		return nil, err
	}

	// 将数据库结果缓存到Redis
	for _, food := range foods {
		foodJSON, err := json.Marshal(food)
		if err != nil {
			r.log.WithContext(ctx).Errorf("Redis: json marshal food error: %v", err)
			continue
		}

		key := fmt.Sprintf("food:fid:%s", food.FID)
		err = r.data.redis.Set(ctx, key, foodJSON, 24*time.Hour).Err()
		if err != nil {
			r.log.WithContext(ctx).Errorf("Redis: set food detail error: %v", err)
		}

		err = r.data.redis.SAdd(ctx, "food:list", food.FID).Err()
		if err != nil {
			r.log.WithContext(ctx).Errorf("Redis: add to food list error: %v", err)
		}
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
	// 尝试从Redis获取食物列表
	foodIDs, err := r.data.redis.SMembers(ctx, "food:list").Result()
	if err == nil && len(foodIDs) > 0 {
		r.log.WithContext(ctx).Info("GetRandomFood: using Redis cache")

		// 随机选择一个FID
		rand.Seed(time.Now().UnixNano())
		randomFID := foodIDs[rand.Intn(len(foodIDs))]

		// 获取对应的食物详情
		key := fmt.Sprintf("food:fid:%s", randomFID)
		foodJSON, err := r.data.redis.Get(ctx, key).Result()
		if err == nil {
			var food Food
			if err := json.Unmarshal([]byte(foodJSON), &food); err == nil {
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
		}
		// 如果从Redis获取失败，回退到数据库查询
	}

	// 从数据库获取
	var foods []Food
	if err := r.data.db.WithContext(ctx).Find(&foods).Error; err != nil {
		r.log.WithContext(ctx).Errorf("GetRandomFood - find error: %v", err)
		return nil, err
	}

	if len(foods) == 0 {
		r.log.WithContext(ctx).Info("GetRandomFood - no food found")
		return nil, nil // 或者返回一个特定的错误，如 ErrFoodNotFound
	}

	// 将数据库结果缓存到Redis
	for _, food := range foods {
		foodJSON, err := json.Marshal(food)
		if err != nil {
			r.log.WithContext(ctx).Errorf("Redis: json marshal food error: %v", err)
			continue
		}

		key := fmt.Sprintf("food:fid:%s", food.FID)
		err = r.data.redis.Set(ctx, key, foodJSON, 24*time.Hour).Err()
		if err != nil {
			r.log.WithContext(ctx).Errorf("Redis: set food detail error: %v", err)
		}

		err = r.data.redis.SAdd(ctx, "food:list", food.FID).Err()
		if err != nil {
			r.log.WithContext(ctx).Errorf("Redis: add to food list error: %v", err)
		}
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
