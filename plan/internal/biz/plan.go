package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
)

type Plan struct {
	ID         string
	UID        string
	Title      string
	City       string
	StartDate  time.Time
	EndDate    time.Time
	Budget     int
	Visibility string
	Status     string
	Cover      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Days       []*Day
}

type Day struct {
	ID     string
	PlanID string
	DayNo  int
	Date   time.Time
	Title  string
	Items  []*Item
}

type Item struct {
	ID        string
	PlanID    string
	DayID     string
	ItemType  string
	ItemID    string
	StartTime string
	EndTime   string
	Note      string
}

type PlanRepo interface {
	Create(ctx context.Context, plan *Plan) (*Plan, error)
	FindByID(ctx context.Context, id string) (*Plan, error)
	List(ctx context.Context, uid, scope string, page, limit int, summaryOnly bool) ([]*Plan, int64, error)
	Update(ctx context.Context, plan *Plan) (*Plan, error)
	Publish(ctx context.Context, uid, id string) error
	AddItem(ctx context.Context, uid string, dayNo int, item *Item) (*Plan, error)
	DeleteItem(ctx context.Context, uid, planID, itemID string) error
	Delete(ctx context.Context, uid, id string) error
}

type PlanUsecase struct {
	repo PlanRepo
	log  *log.Helper
}

func NewPlanUsecase(repo PlanRepo, logger log.Logger) *PlanUsecase {
	return &PlanUsecase{repo: repo, log: log.NewHelper(logger)}
}

func (uc *PlanUsecase) Create(ctx context.Context, uid string, title, city, start, end, visibility, cover string, budget int) (*Plan, error) {
	startDate, err := time.Parse("2006-01-02", start)
	if err != nil {
		return nil, ErrInvalidDate
	}
	endDate, err := time.Parse("2006-01-02", end)
	if err != nil {
		return nil, ErrInvalidDate
	}
	if endDate.Before(startDate) {
		return nil, ErrInvalidDateRange
	}
	if endDate.Sub(startDate) > 29*24*time.Hour {
		return nil, ErrPlanTooLong
	}
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 100 {
		return nil, ErrInvalidArgument
	}
	if visibility == "" {
		visibility = "private"
	}
	if visibility != "private" && visibility != "public" {
		return nil, ErrInvalidVisibility
	}

	plan := &Plan{
		ID:         uuid.New().String(),
		UID:        uid,
		Title:      title,
		City:       strings.TrimSpace(city),
		StartDate:  startDate,
		EndDate:    endDate,
		Budget:     budget,
		Visibility: visibility,
		Status:     "draft",
		Cover:      cover,
	}
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		plan.Days = append(plan.Days, &Day{
			ID:     uuid.New().String(),
			PlanID: plan.ID,
			DayNo:  len(plan.Days) + 1,
			Date:   d,
			Title:  fmt.Sprintf("Day %d", len(plan.Days)+1),
		})
	}
	created, err := uc.repo.Create(ctx, plan)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("planUsecase: create failed: %v", err)
		return nil, ErrCreateFailed
	}
	return created, nil
}

func (uc *PlanUsecase) Update(ctx context.Context, uid, id, title, city, visibility, cover string, budget int) (*Plan, error) {
	if title = strings.TrimSpace(title); title == "" || len([]rune(title)) > 100 {
		return nil, ErrInvalidArgument
	}
	if visibility != "" && visibility != "private" && visibility != "public" {
		return nil, ErrInvalidVisibility
	}
	updated, err := uc.repo.Update(ctx, &Plan{
		ID: id, UID: uid, Title: title, City: strings.TrimSpace(city),
		Budget: budget, Visibility: visibility, Cover: cover,
	})
	return updated, uc.mutationError(ctx, err)
}

func (uc *PlanUsecase) Get(ctx context.Context, id, uid string) (*Plan, error) {
	plan, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrPlanNotFound
	}
	if plan.UID != uid && !(plan.Status == "published" && plan.Visibility == "public") {
		return nil, ErrPlanNotFound
	}
	return plan, nil
}

func (uc *PlanUsecase) List(ctx context.Context, uid, scope string, page, limit int, summaryOnly bool) ([]*Plan, int64, error) {
	if scope == "" {
		scope = "public"
	}
	if scope != "mine" && scope != "public" {
		return nil, 0, ErrInvalidArgument
	}
	if scope == "mine" && uid == "" {
		return nil, 0, ErrUnauthorized
	}
	if page < 1 {
		page = 1
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	return uc.repo.List(ctx, uid, scope, page, limit, summaryOnly)
}

func (uc *PlanUsecase) Publish(ctx context.Context, uid, id string) (string, error) {
	if err := uc.repo.Publish(ctx, uid, id); err != nil {
		return "", uc.mutationError(ctx, err)
	}
	return "published", nil
}

func validateItemType(t string) bool {
	switch t {
	case "scene", "food", "article":
		return true
	default:
		return false
	}
}

func validClock(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	_, err := time.Parse("15:04", s)
	return err == nil
}

func (uc *PlanUsecase) AddItem(ctx context.Context, uid, planID string, dayNo int, itemType, itemID, start, end, note string) (*Plan, error) {
	if !validateItemType(itemType) || strings.TrimSpace(itemID) == "" {
		return nil, ErrInvalidArgument
	}
	if !validClock(start) || !validClock(end) || start >= end {
		return nil, ErrInvalidTime
	}
	updated, err := uc.repo.AddItem(ctx, uid, dayNo, &Item{
		ID:        uuid.New().String(),
		PlanID:    planID,
		ItemType:  itemType,
		ItemID:    itemID,
		StartTime: start,
		EndTime:   end,
		Note:      note,
	})
	return updated, uc.mutationError(ctx, err)
}

func (uc *PlanUsecase) DeleteItem(ctx context.Context, uid, planID, itemID string) error {
	return uc.mutationError(ctx, uc.repo.DeleteItem(ctx, uid, planID, itemID))
}

func (uc *PlanUsecase) Delete(ctx context.Context, uid, id string) error {
	return uc.mutationError(ctx, uc.repo.Delete(ctx, uid, id))
}

func (uc *PlanUsecase) mutationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var businessErr *kerrors.Error
	if errors.As(err, &businessErr) {
		return businessErr
	}
	uc.log.WithContext(ctx).Errorf("planUsecase: write failed: %v", err)
	return ErrUpdateFailed
}
