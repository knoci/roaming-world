package data

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/knoci/roaming-world/food/internal/biz"
	"gorm.io/gorm"
)

// Food 数据库模型定义。
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
		r.log.WithContext(ctx).Errorf("foodRepo: CreateFood error: %v", err)
		error := r.data.LogError(ctx, "food", err.Error(), "db.Create", food)
		if error != nil {
			r.log.WithContext(ctx).Errorf("foodRepo: log creation error: %v", error)
		}
		return nil, err
	}

	sql := `INSERT INTO foods (fid, name, view, describe, recipe, article, location) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	params := []any{food.FID, food.Name, food.View, food.Describe, food.Recipe, food.Article, food.Location}
	error := r.data.LogSQLAudit(ctx, "food", sql, params)
	if error != nil {
		r.log.WithContext(ctx).Errorf("foodRepo: log SQL audit: %v", error)
	}

	return toBizFood(&food), nil
}

func (r *foodRepo) GetFoodList(ctx context.Context, page, limit int) ([]*biz.Food, int64, error) {
	page, limit = biz.NormalizeFoodPagination(page, limit)

	query := r.data.db.WithContext(ctx).Model(&Food{})
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var foods []Food
	if err := query.Order("created_at DESC NULLS LAST, fid DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&foods).Error; err != nil {
		return nil, 0, err
	}

	items := make([]*biz.Food, 0, len(foods))
	for i := range foods {
		items = append(items, toBizFood(&foods[i]))
	}
	return items, total, nil
}

func (r *foodRepo) GetRandomFood(ctx context.Context) (*biz.Food, error) {
	var total int64
	if err := r.data.db.WithContext(ctx).Model(&Food{}).Count(&total).Error; err != nil {
		return nil, err
	}
	if total <= 0 {
		return nil, nil
	}

	offset := int(rand.Int63n(total))
	var food Food
	err := r.data.db.WithContext(ctx).Order("fid").Offset(offset).Limit(1).Take(&food).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toBizFood(&food), nil
}

func toBizFood(row *Food) *biz.Food {
	return &biz.Food{
		FID: row.FID, View: row.View, Name: row.Name, Describe: row.Describe,
		Recipe: row.Recipe, Article: row.Article, Location: row.Location,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
