package data

import (
	"context"
	"errors"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/knoci/roaming-world/plan/internal/biz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	days := make([]TravelPlanDay, 0, len(plan.Days))
	for _, day := range plan.Days {
		days = append(days, TravelPlanDay{ID: day.ID, PlanID: plan.ID, DayNo: day.DayNo, Date: day.Date, Title: day.Title})
	}
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		return tx.Create(&days).Error
	})
	if err != nil {
		return nil, err
	}
	row.Days = days
	return toBizPlan(row), nil
}

func (r *planRepo) FindByID(ctx context.Context, id string) (*biz.Plan, error) {
	return findPlan(r.data.db.WithContext(ctx), id)
}

func withPlanDetails(db *gorm.DB) *gorm.DB {
	return db.
		Preload("Days", func(db *gorm.DB) *gorm.DB { return db.Order("day_no ASC") }).
		Preload("Days.Items", func(db *gorm.DB) *gorm.DB { return db.Order("start_time ASC, end_time ASC, id ASC") })
}

func findPlan(db *gorm.DB, id string) (*biz.Plan, error) {
	var row TravelPlan
	if err := withPlanDetails(db).Where("id = ?", id).Take(&row).Error; err != nil {
		return nil, err
	}
	return toBizPlan(&row), nil
}

func (r *planRepo) List(ctx context.Context, uid, scope string, page, limit int, summaryOnly bool) ([]*biz.Plan, int64, error) {
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
	q = q.Order("updated_at DESC, id DESC").Offset((page - 1) * limit).Limit(limit)
	if !summaryOnly {
		q = withPlanDetails(q)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	plans := make([]*biz.Plan, 0, len(rows))
	for i := range rows {
		plans = append(plans, toBizPlan(&rows[i]))
	}
	return plans, total, nil
}

// Lock the parent before examining or changing any of its state or items.
func (r *planRepo) withLockedPlan(ctx context.Context, uid, id string, draftOnly bool, write func(*gorm.DB, *TravelPlan) error) error {
	return r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row TravelPlan
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "uid", "status").Where("id = ?", id).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return biz.ErrPlanNotFound
		}
		if err != nil {
			return err
		}
		if row.UID != uid {
			return biz.ErrNoPermission
		}
		if draftOnly && row.Status != "draft" {
			return biz.ErrPlanNotEditable
		}
		return write(tx, &row)
	})
}

func (r *planRepo) Update(ctx context.Context, patch *biz.Plan) (*biz.Plan, error) {
	var updated *biz.Plan
	err := r.withLockedPlan(ctx, patch.UID, patch.ID, true, func(tx *gorm.DB, row *TravelPlan) error {
		fields := map[string]any{
			"title": patch.Title, "city": patch.City, "budget": patch.Budget, "cover": patch.Cover,
		}
		if patch.Visibility != "" {
			fields["visibility"] = patch.Visibility
		}
		if err := tx.Model(row).Updates(fields).Error; err != nil {
			return err
		}
		var err error
		updated, err = findPlan(tx, row.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (r *planRepo) Publish(ctx context.Context, uid, id string) error {
	return r.withLockedPlan(ctx, uid, id, true, func(tx *gorm.DB, row *TravelPlan) error {
		var hasItems bool
		if err := tx.Raw(`
			SELECT EXISTS (
				SELECT 1 FROM travel_plan_items AS item
				JOIN travel_plan_days AS day ON day.id = item.day_id AND day.plan_id = item.plan_id
				WHERE item.plan_id = ?
			)
		`, id).Scan(&hasItems).Error; err != nil {
			return err
		}
		if !hasItems {
			return biz.ErrEmptyPlan
		}
		return tx.Model(row).Update("status", "published").Error
	})
}

func (r *planRepo) AddItem(ctx context.Context, uid string, dayNo int, item *biz.Item) (*biz.Plan, error) {
	var updated *biz.Plan
	err := r.withLockedPlan(ctx, uid, item.PlanID, true, func(tx *gorm.DB, plan *TravelPlan) error {
		var day TravelPlanDay
		err := tx.Select("id").Where("plan_id = ? AND day_no = ?", plan.ID, dayNo).Take(&day).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return biz.ErrDayNotFound
		}
		if err != nil {
			return err
		}
		var stats struct {
			NextSortOrder int
			Overlap       bool
		}
		if err := tx.Model(&TravelPlanItem{}).Where("day_id = ?", day.ID).
			Select("COALESCE(MAX(sort_order), 0) + 1 AS next_sort_order, COALESCE(bool_or(start_time < ? AND end_time > ?), false) AS overlap",
				item.EndTime, item.StartTime).Scan(&stats).Error; err != nil {
			return err
		}
		if stats.Overlap {
			return biz.ErrTimeOverlap
		}
		if err := tx.Create(&TravelPlanItem{
			ID: item.ID, PlanID: plan.ID, DayID: day.ID,
			ItemType: item.ItemType, ItemID: item.ItemID,
			StartTime: item.StartTime, EndTime: item.EndTime, Note: item.Note, SortOrder: stats.NextSortOrder,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(plan).Update("updated_at", time.Now()).Error; err != nil {
			return err
		}
		updated, err = findPlan(tx, plan.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (r *planRepo) DeleteItem(ctx context.Context, uid, planID, itemID string) error {
	return r.withLockedPlan(ctx, uid, planID, true, func(tx *gorm.DB, plan *TravelPlan) error {
		res := tx.Where("id = ? AND plan_id = ?", itemID, planID).Delete(&TravelPlanItem{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return biz.ErrItemNotFound
		}
		return tx.Model(plan).Update("updated_at", time.Now()).Error
	})
}

func (r *planRepo) Delete(ctx context.Context, uid, id string) error {
	return r.withLockedPlan(ctx, uid, id, false, func(tx *gorm.DB, plan *TravelPlan) error {
		if err := tx.Where("plan_id = ?", id).Delete(&TravelPlanItem{}).Error; err != nil {
			return err
		}
		if err := tx.Where("plan_id = ?", id).Delete(&TravelPlanDay{}).Error; err != nil {
			return err
		}
		return tx.Delete(plan).Error
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
				StartTime: it.StartTime, EndTime: it.EndTime, Note: it.Note,
			})
		}
		p.Days = append(p.Days, day)
	}
	return p
}
