package biz

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	MaxTitleRunes   = 100
	MaxCityRunes    = 50
	MaxCoverRunes   = 255
	MaxItemIDRunes  = 36
	MaxNoteRunes    = 1000
	MaxItemsPerDay  = 50
	MaxItemsPerPlan = 300
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

func validCoverURL(cover string) bool {
	if cover == "" {
		return true
	}
	if utf8.RuneCountInString(cover) > MaxCoverRunes {
		return false
	}
	if strings.HasPrefix(cover, "/") {
		return !strings.HasPrefix(cover, "//")
	}
	u, err := url.Parse(cover)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Host != ""
}

// CheckItemLimits enforces per-day and per-plan item caps.
func CheckItemLimits(dayItems, totalItems int64) error {
	if dayItems >= MaxItemsPerDay || totalItems >= MaxItemsPerPlan {
		return ErrTooManyItems
	}
	return nil
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
	if budget < 0 {
		return nil, ErrInvalidArgument
	}
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > MaxTitleRunes {
		return nil, ErrInvalidArgument
	}
	city = strings.TrimSpace(city)
	if utf8.RuneCountInString(city) > MaxCityRunes {
		return nil, ErrInvalidArgument
	}
	cover = strings.TrimSpace(cover)
	if !validCoverURL(cover) {
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
		City:       city,
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

type readinessPinger interface {
	Ping(ctx context.Context) error
}

// Ping checks backing repository connectivity when supported by the repo implementation.
func (uc *PlanUsecase) Ping(ctx context.Context) error {
	if uc == nil || uc.repo == nil {
		return errors.New("plan repository is not initialized")
	}
	if pinger, ok := uc.repo.(readinessPinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

// BuildUpdateFields computes the partial update field map for an existing plan under lock.
//   - On draft plans: preserves existing Title, City, Cover, Visibility, and Budget when omitted ("" or 0).
//   - On published plans: allows toggling Visibility (and safely updating City/Cover) while keeping
//     structural Title/Budget changes draft-only (returning ErrPlanNotEditable).
func BuildUpdateFields(existing, patch *Plan) (map[string]any, error) {
	if existing == nil || patch == nil {
		return nil, ErrInvalidArgument
	}

	switch existing.Status {
	case "draft":
		fields := make(map[string]any)
		if patch.Title != "" {
			fields["title"] = patch.Title
		}
		if patch.City != "" {
			fields["city"] = patch.City
		}
		if patch.Cover != "" {
			fields["cover"] = patch.Cover
		}
		if patch.Visibility != "" {
			fields["visibility"] = patch.Visibility
		}
		if patch.Budget > 0 {
			fields["budget"] = patch.Budget
		}
		if len(fields) == 0 {
			return nil, ErrInvalidArgument
		}
		return fields, nil

	case "published":
		if patch.Title != "" && patch.Title != existing.Title {
			return nil, ErrPlanNotEditable
		}
		if patch.Budget > 0 && patch.Budget != existing.Budget {
			return nil, ErrPlanNotEditable
		}
		fields := make(map[string]any)
		if patch.Visibility != "" {
			fields["visibility"] = patch.Visibility
		}
		if patch.Cover != "" {
			fields["cover"] = patch.Cover
		}
		if patch.City != "" {
			fields["city"] = patch.City
		}
		if len(fields) == 0 {
			return nil, ErrPlanNotEditable
		}
		return fields, nil

	default:
		return nil, ErrPlanNotEditable
	}
}

func (uc *PlanUsecase) Update(ctx context.Context, uid, id, title, city, visibility, cover string, budget int) (*Plan, error) {
	if budget < 0 {
		return nil, ErrInvalidArgument
	}
	title = strings.TrimSpace(title)
	if title != "" && utf8.RuneCountInString(title) > MaxTitleRunes {
		return nil, ErrInvalidArgument
	}
	city = strings.TrimSpace(city)
	if city != "" && utf8.RuneCountInString(city) > MaxCityRunes {
		return nil, ErrInvalidArgument
	}
	cover = strings.TrimSpace(cover)
	if !validCoverURL(cover) {
		return nil, ErrInvalidArgument
	}
	visibility = strings.TrimSpace(visibility)
	if visibility != "" && visibility != "private" && visibility != "public" {
		return nil, ErrInvalidVisibility
	}
	if title == "" && city == "" && visibility == "" && cover == "" && budget == 0 {
		return nil, ErrInvalidArgument
	}
	updated, err := uc.repo.Update(ctx, &Plan{
		ID: id, UID: uid, Title: title, City: city,
		Budget: budget, Visibility: visibility, Cover: cover,
	})
	return updated, uc.mutationError(ctx, err)
}

func (uc *PlanUsecase) Get(ctx context.Context, id, uid string) (*Plan, error) {
	plan, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrPlanNotFound) {
			return nil, ErrPlanNotFound
		}
		uc.log.WithContext(ctx).Errorf("planUsecase: get plan %s failed: %v", id, err)
		return nil, ErrInternalError
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
	plans, total, err := uc.repo.List(ctx, uid, scope, page, limit, summaryOnly)
	if err != nil {
		var businessErr *kerrors.Error
		if errors.As(err, &businessErr) {
			return nil, 0, businessErr
		}
		uc.log.WithContext(ctx).Errorf("planUsecase: list failed: %v", err)
		return nil, 0, ErrInternalError
	}
	return plans, total, nil
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
	itemID = strings.TrimSpace(itemID)
	if !validateItemType(itemType) || itemID == "" || utf8.RuneCountInString(itemID) > MaxItemIDRunes {
		return nil, ErrInvalidArgument
	}
	if utf8.RuneCountInString(note) > MaxNoteRunes {
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
