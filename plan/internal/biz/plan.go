package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	SortOrder int
}

type PlanRepo interface {
	Create(ctx context.Context, plan *Plan) (*Plan, error)
	FindByID(ctx context.Context, id string) (*Plan, error)
	List(ctx context.Context, uid, scope string, page, limit int) ([]*Plan, int64, error)
	Update(ctx context.Context, plan *Plan) (*Plan, error)
	Publish(ctx context.Context, id string) error
	AddItem(ctx context.Context, item *Item) (*Plan, error)
	DeleteItem(ctx context.Context, planID, itemID string) error
	Delete(ctx context.Context, id string) error
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
	plan, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrPlanNotFound
	}
	if plan.UID != uid {
		return nil, ErrNoPermission
	}
	if plan.Status != "draft" {
		return nil, ErrPlanNotEditable
	}
	if title = strings.TrimSpace(title); title == "" || len([]rune(title)) > 100 {
		return nil, ErrInvalidArgument
	}
	if visibility == "" {
		visibility = plan.Visibility
	}
	if visibility != "private" && visibility != "public" {
		return nil, ErrInvalidVisibility
	}
	plan.Title = title
	plan.City = strings.TrimSpace(city)
	plan.Budget = budget
	plan.Visibility = visibility
	plan.Cover = cover
	updated, err := uc.repo.Update(ctx, plan)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("planUsecase: update failed: %v", err)
		return nil, ErrUpdateFailed
	}
	return updated, nil
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

func (uc *PlanUsecase) List(ctx context.Context, uid, scope string, page, limit int) ([]*Plan, int64, error) {
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
	return uc.repo.List(ctx, uid, scope, page, limit)
}

func (uc *PlanUsecase) Publish(ctx context.Context, uid, id string) (string, error) {
	plan, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return "", ErrPlanNotFound
	}
	if plan.UID != uid {
		return "", ErrNoPermission
	}
	if plan.Status != "draft" {
		return "", ErrPlanNotEditable
	}
	itemCount := 0
	for _, day := range plan.Days {
		itemCount += len(day.Items)
	}
	if itemCount == 0 {
		return "", ErrEmptyPlan
	}
	if err := uc.repo.Publish(ctx, id); err != nil {
		return "", ErrUpdateFailed
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

func overlaps(aStart, aEnd, bStart, bEnd string) bool {
	return aStart < bEnd && bStart < aEnd
}

func (uc *PlanUsecase) AddItem(ctx context.Context, uid, planID string, dayNo int, itemType, itemID, start, end, note string) (*Plan, error) {
	plan, err := uc.repo.FindByID(ctx, planID)
	if err != nil {
		return nil, ErrPlanNotFound
	}
	if plan.UID != uid {
		return nil, ErrNoPermission
	}
	if plan.Status != "draft" {
		return nil, ErrPlanNotEditable
	}
	if !validateItemType(itemType) || strings.TrimSpace(itemID) == "" {
		return nil, ErrInvalidArgument
	}
	if !validClock(start) || !validClock(end) || start >= end {
		return nil, ErrInvalidTime
	}
	var day *Day
	for _, d := range plan.Days {
		if d.DayNo == dayNo {
			day = d
			break
		}
	}
	if day == nil {
		return nil, ErrDayNotFound
	}
	for _, it := range day.Items {
		if overlaps(start, end, it.StartTime, it.EndTime) {
			return nil, ErrTimeOverlap
		}
	}
	item := &Item{
		ID:        uuid.New().String(),
		PlanID:    plan.ID,
		DayID:     day.ID,
		ItemType:  itemType,
		ItemID:    itemID,
		StartTime: start,
		EndTime:   end,
		Note:      note,
		SortOrder: len(day.Items) + 1,
	}
	updated, err := uc.repo.AddItem(ctx, item)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("planUsecase: add item failed: %v", err)
		return nil, ErrUpdateFailed
	}
	return updated, nil
}

func (uc *PlanUsecase) DeleteItem(ctx context.Context, uid, planID, itemID string) error {
	plan, err := uc.repo.FindByID(ctx, planID)
	if err != nil {
		return ErrPlanNotFound
	}
	if plan.UID != uid {
		return ErrNoPermission
	}
	if plan.Status != "draft" {
		return ErrPlanNotEditable
	}
	if err := uc.repo.DeleteItem(ctx, planID, itemID); err != nil {
		return ErrUpdateFailed
	}
	return nil
}

func (uc *PlanUsecase) Delete(ctx context.Context, uid, id string) error {
	plan, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return ErrPlanNotFound
	}
	if plan.UID != uid {
		return ErrNoPermission
	}
	if err := uc.repo.Delete(ctx, id); err != nil {
		return ErrUpdateFailed
	}
	return nil
}
