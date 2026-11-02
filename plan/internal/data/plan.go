package data

import (
	"context"
	"errors"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/knoci/roaming-world/plan/internal/biz"
	"gorm.io/gorm"
)

type TravelPlan struct {
	ID         string          `gorm:"primaryKey;type:varchar(36)" json:"id"`
	UID        string          `gorm:"type:varchar(36);not null;index:idx_travel_plans_uid" json:"uid"`
	Title      string          `gorm:"type:varchar(100);not null" json:"title"`
	City       string          `gorm:"type:varchar(50)" json:"city"`
	StartDate  time.Time       `gorm:"type:date;not null" json:"start_date"`
	EndDate    time.Time       `gorm:"type:date;not null" json:"end_date"`
	Budget     int             `gorm:"default:0" json:"budget"`
	Visibility string          `gorm:"type:varchar(20);not null;default:private" json:"visibility"`
	Status     string          `gorm:"type:varchar(20);not null;default:draft;index:idx_travel_plans_status" json:"status"`
	Cover      string          `gorm:"type:varchar(255)" json:"cover"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Days       []TravelPlanDay `gorm:"foreignKey:PlanID" json:"days"`
}

type TravelPlanDay struct {
	ID     string           `gorm:"primaryKey;type:varchar(36)" json:"id"`
	PlanID string           `gorm:"type:varchar(36);not null;index:idx_travel_plan_days_plan;uniqueIndex:uk_plan_day,priority:1" json:"plan_id"`
	DayNo  int              `gorm:"not null;uniqueIndex:uk_plan_day,priority:2" json:"day_no"`
	Date   time.Time        `gorm:"type:date;not null" json:"date"`
	Title  string           `gorm:"type:varchar(100)" json:"title"`
	Items  []TravelPlanItem `gorm:"foreignKey:DayID" json:"items"`
}

type TravelPlanItem struct {
	ID        string `gorm:"primaryKey;type:varchar(36)" json:"id"`
	PlanID    string `gorm:"type:varchar(36);not null;index:idx_travel_plan_items_plan" json:"plan_id"`
	DayID     string `gorm:"type:varchar(36);not null;index:idx_travel_plan_items_day" json:"day_id"`
	ItemType  string `gorm:"type:varchar(20);not null" json:"item_type"`
	ItemID    string `gorm:"type:varchar(36);not null" json:"item_id"`
	StartTime string `gorm:"type:varchar(5);not null" json:"start_time"`
	EndTime   string `gorm:"type:varchar(5);not null" json:"end_time"`
	Note      string `gorm:"type:text" json:"note"`
	SortOrder int    `gorm:"not null;default:0" json:"sort_order"`
}

type planRepo struct {
	data *Data
	log  *log.Helper
}

func NewPlanRepo(data *Data, logger log.Logger) biz.PlanRepo {
	return &planRepo{data: data, log: log.NewHelper(logger)}
}

func (r *planRepo) Create(ctx context.Context, plan *biz.Plan) (*biz.Plan, error) {
	row := &TravelPlan{
		ID: plan.ID, UID: plan.UID, Title: plan.Title, City: plan.City,
		StartDate: plan.StartDate, EndDate: plan.EndDate, Budget: plan.Budget,
		Visibility: plan.Visibility, Status: plan.Status, Cover: plan.Cover,
	}
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		for _, day := range plan.Days {
			dayRow := &TravelPlanDay{ID: day.ID, PlanID: plan.ID, DayNo: day.DayNo, Date: day.Date, Title: day.Title}
			if err := tx.Create(dayRow).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.FindByID(ctx, plan.ID)
}

func (r *planRepo) FindByID(ctx context.Context, id string) (*biz.Plan, error) {
	var row TravelPlan
	if err := r.data.db.WithContext(ctx).
		Preload("Days", func(db *gorm.DB) *gorm.DB { return db.Order("day_no ASC") }).
		Preload("Days.Items", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC") }).
		Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return toBizPlan(&row), nil
}

func (r *planRepo) List(ctx context.Context, uid, scope string, page, limit int) ([]*biz.Plan, int64, error) {
	q := r.data.db.WithContext(ctx).Model(&TravelPlan{})
	if scope == "mine" {
		q = q.Where("uid = ?", uid)
	} else {
		q = q.Where("status = ? AND visibility = ?", "published", "public")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []TravelPlan
	if err := q.Order("updated_at DESC").Offset((page-1)*limit).Limit(limit).
		Preload("Days", func(db *gorm.DB) *gorm.DB { return db.Order("day_no ASC") }).
		Preload("Days.Items", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC") }).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	plans := make([]*biz.Plan, 0, len(rows))
	for i := range rows {
		plans = append(plans, toBizPlan(&rows[i]))
	}
	return plans, total, nil
}

func (r *planRepo) Update(ctx context.Context, plan *biz.Plan) (*biz.Plan, error) {
	err := r.data.db.WithContext(ctx).Model(&TravelPlan{}).Where("id = ?", plan.ID).Updates(map[string]any{
		"title": plan.Title, "city": plan.City, "budget": plan.Budget,
		"visibility": plan.Visibility, "cover": plan.Cover, "updated_at": time.Now(),
	}).Error
	if err != nil {
		return nil, err
	}
	return r.FindByID(ctx, plan.ID)
}

func (r *planRepo) Publish(ctx context.Context, id string) error {
	return r.data.db.WithContext(ctx).Model(&TravelPlan{}).Where("id = ?", id).
		Updates(map[string]any{"status": "published", "updated_at": time.Now()}).Error
}

func (r *planRepo) AddItem(ctx context.Context, item *biz.Item) (*biz.Plan, error) {
	row := &TravelPlanItem{
		ID: item.ID, PlanID: item.PlanID, DayID: item.DayID,
		ItemType: item.ItemType, ItemID: item.ItemID,
		StartTime: item.StartTime, EndTime: item.EndTime, Note: item.Note, SortOrder: item.SortOrder,
	}
	if err := r.data.db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, err
	}
	return r.FindByID(ctx, item.PlanID)
}

func (r *planRepo) DeleteItem(ctx context.Context, planID, itemID string) error {
	res := r.data.db.WithContext(ctx).Where("id = ? AND plan_id = ?", itemID, planID).Delete(&TravelPlanItem{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *planRepo) Delete(ctx context.Context, id string) error {
	return r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("plan_id = ?", id).Delete(&TravelPlanItem{}).Error; err != nil {
			return err
		}
		if err := tx.Where("plan_id = ?", id).Delete(&TravelPlanDay{}).Error; err != nil {
			return err
		}
		res := tx.Where("id = ?", id).Delete(&TravelPlan{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errors.New("plan not found")
		}
		return nil
	})
}

func toBizPlan(row *TravelPlan) *biz.Plan {
	p := &biz.Plan{
		ID: row.ID, UID: row.UID, Title: row.Title, City: row.City,
		StartDate: row.StartDate, EndDate: row.EndDate, Budget: row.Budget,
		Visibility: row.Visibility, Status: row.Status, Cover: row.Cover,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	for i := range row.Days {
		d := &row.Days[i]
		day := &biz.Day{ID: d.ID, PlanID: d.PlanID, DayNo: d.DayNo, Date: d.Date, Title: d.Title}
		for j := range d.Items {
			it := &d.Items[j]
			day.Items = append(day.Items, &biz.Item{
				ID: it.ID, PlanID: it.PlanID, DayID: it.DayID,
				ItemType: it.ItemType, ItemID: it.ItemID,
				StartTime: it.StartTime, EndTime: it.EndTime, Note: it.Note, SortOrder: it.SortOrder,
			})
		}
		p.Days = append(p.Days, day)
	}
	return p
}
