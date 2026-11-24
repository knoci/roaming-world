package biz

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/gorm"
)

type fakePlanRepo struct {
	createFn     func(ctx context.Context, plan *Plan) (*Plan, error)
	findByIDFn   func(ctx context.Context, id string) (*Plan, error)
	listFn       func(ctx context.Context, uid, scope string, page, limit int, summaryOnly bool) ([]*Plan, int64, error)
	updateFn     func(ctx context.Context, plan *Plan) (*Plan, error)
	publishFn    func(ctx context.Context, uid, id string) error
	addItemFn    func(ctx context.Context, uid string, dayNo int, item *Item) (*Plan, error)
	deleteItemFn func(ctx context.Context, uid, planID, itemID string) error
	deleteFn     func(ctx context.Context, uid, id string) error
}

func (f *fakePlanRepo) Create(ctx context.Context, plan *Plan) (*Plan, error) {
	if f.createFn != nil {
		return f.createFn(ctx, plan)
	}
	return plan, nil
}

func (f *fakePlanRepo) FindByID(ctx context.Context, id string) (*Plan, error) {
	if f.findByIDFn != nil {
		return f.findByIDFn(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakePlanRepo) List(ctx context.Context, uid, scope string, page, limit int, summaryOnly bool) ([]*Plan, int64, error) {
	if f.listFn != nil {
		return f.listFn(ctx, uid, scope, page, limit, summaryOnly)
	}
	return nil, 0, nil
}

func (f *fakePlanRepo) Update(ctx context.Context, plan *Plan) (*Plan, error) {
	if f.updateFn != nil {
		return f.updateFn(ctx, plan)
	}
	return plan, nil
}

func (f *fakePlanRepo) Publish(ctx context.Context, uid, id string) error {
	if f.publishFn != nil {
		return f.publishFn(ctx, uid, id)
	}
	return nil
}

func (f *fakePlanRepo) AddItem(ctx context.Context, uid string, dayNo int, item *Item) (*Plan, error) {
	if f.addItemFn != nil {
		return f.addItemFn(ctx, uid, dayNo, item)
	}
	return &Plan{ID: item.PlanID, UID: uid}, nil
}

func (f *fakePlanRepo) DeleteItem(ctx context.Context, uid, planID, itemID string) error {
	if f.deleteItemFn != nil {
		return f.deleteItemFn(ctx, uid, planID, itemID)
	}
	return nil
}

func (f *fakePlanRepo) Delete(ctx context.Context, uid, id string) error {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, uid, id)
	}
	return nil
}

func TestPlanUsecase_GetErrorMapping(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)

	t.Run("returns 404 on gorm.ErrRecordNotFound", func(t *testing.T) {
		uc := NewPlanUsecase(&fakePlanRepo{
			findByIDFn: func(context.Context, string) (*Plan, error) {
				return nil, gorm.ErrRecordNotFound
			},
		}, logger)

		_, err := uc.Get(context.Background(), "plan-1", "user-1")
		if !errors.Is(err, ErrPlanNotFound) {
			t.Fatalf("expected ErrPlanNotFound, got %v", err)
		}
		if ke := kerrors.FromError(err); ke.Code != 404 {
			t.Fatalf("expected 404 code, got %d", ke.Code)
		}
	})

	t.Run("returns 404 on ErrPlanNotFound", func(t *testing.T) {
		uc := NewPlanUsecase(&fakePlanRepo{
			findByIDFn: func(context.Context, string) (*Plan, error) {
				return nil, ErrPlanNotFound
			},
		}, logger)

		_, err := uc.Get(context.Background(), "plan-1", "user-1")
		if !errors.Is(err, ErrPlanNotFound) {
			t.Fatalf("expected ErrPlanNotFound, got %v", err)
		}
	})

	t.Run("returns 500 ErrInternalError on DB failure", func(t *testing.T) {
		uc := NewPlanUsecase(&fakePlanRepo{
			findByIDFn: func(context.Context, string) (*Plan, error) {
				return nil, errors.New("pq: connection refused to postgres:5432")
			},
		}, logger)

		_, err := uc.Get(context.Background(), "plan-1", "user-1")
		if !errors.Is(err, ErrInternalError) {
			t.Fatalf("expected ErrInternalError, got %v", err)
		}
		ke := kerrors.FromError(err)
		if ke.Code != 500 {
			t.Fatalf("expected 500 code, got %d", ke.Code)
		}
		if strings.Contains(ke.Message, "postgres") {
			t.Fatalf("internal DB details leaked in error message: %q", ke.Message)
		}
	})

	t.Run("hides private draft plan from non-owner as 404", func(t *testing.T) {
		uc := NewPlanUsecase(&fakePlanRepo{
			findByIDFn: func(context.Context, string) (*Plan, error) {
				return &Plan{ID: "plan-1", UID: "owner-1", Status: "draft", Visibility: "private"}, nil
			},
		}, logger)

		_, err := uc.Get(context.Background(), "plan-1", "other-user")
		if !errors.Is(err, ErrPlanNotFound) {
			t.Fatalf("expected ErrPlanNotFound, got %v", err)
		}
	})
}

func TestPlanUsecase_ListErrorMapping(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	uc := NewPlanUsecase(&fakePlanRepo{
		listFn: func(context.Context, string, string, int, int, bool) ([]*Plan, int64, error) {
			return nil, 0, errors.New("ERROR: relation \"travel_plans\" does not exist (SQLSTATE 42P01)")
		},
	}, logger)

	_, _, err := uc.List(context.Background(), "", "public", 1, 10, true)
	if !errors.Is(err, ErrInternalError) {
		t.Fatalf("expected ErrInternalError, got %v", err)
	}
	ke := kerrors.FromError(err)
	if ke.Code != 500 {
		t.Fatalf("expected 500 code, got %d", ke.Code)
	}
	if strings.Contains(ke.Message, "SQLSTATE") || strings.Contains(ke.Message, "travel_plans") {
		t.Fatalf("raw SQL details leaked: %q", ke.Message)
	}
}

func TestPlanUsecase_CreateValidation(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	uc := NewPlanUsecase(&fakePlanRepo{}, logger)

	cases := []struct {
		name       string
		title      string
		city       string
		cover      string
		budget     int
		wantErr    error
	}{
		{
			name:    "negative budget rejected",
			title:   "Trip",
			city:    "Tokyo",
			cover:   "https://cdn.example.com/a.jpg",
			budget:  -1,
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "oversized title rejected",
			title:   strings.Repeat("旅", 101),
			city:    "Tokyo",
			cover:   "",
			budget:  1000,
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "oversized city rejected",
			title:   "Trip",
			city:    strings.Repeat("京", 51),
			cover:   "",
			budget:  1000,
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "oversized cover rejected",
			title:   "Trip",
			city:    "Tokyo",
			cover:   "https://example.com/" + strings.Repeat("a", 240),
			budget:  1000,
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "javascript cover scheme rejected",
			title:   "Trip",
			city:    "Tokyo",
			cover:   "javascript:alert(1)",
			budget:  1000,
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "data cover scheme rejected",
			title:   "Trip",
			city:    "Tokyo",
			cover:   "data:text/html,<script>alert(1)</script>",
			budget:  1000,
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "protocol-relative cover rejected",
			title:   "Trip",
			city:    "Tokyo",
			cover:   "//evil.example.com/x.png",
			budget:  1000,
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "valid https cover accepted",
			title:   "Trip",
			city:    "Tokyo",
			cover:   "https://cdn.example.com/cover.png",
			budget:  0,
			wantErr: nil,
		},
		{
			name:    "valid relative cover accepted",
			title:   "Trip",
			city:    "Tokyo",
			cover:   "/static/covers/tokyo.png",
			budget:  500,
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := uc.Create(context.Background(), "u1", tc.title, tc.city, "2030-05-01", "2030-05-02", "public", tc.cover, tc.budget)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil || len(got.Days) != 2 {
				t.Fatalf("unexpected plan: %+v", got)
			}
		})
	}
}

func TestPlanUsecase_UpdateAndAddItemValidation(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	var capturedItem *Item
	uc := NewPlanUsecase(&fakePlanRepo{
		addItemFn: func(_ context.Context, uid string, dayNo int, item *Item) (*Plan, error) {
			capturedItem = item
			return &Plan{ID: item.PlanID, UID: uid, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
		},
	}, logger)

	if _, err := uc.Update(context.Background(), "u1", "p1", "Title", "City", "public", "ftp://bad/img.png", 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for bad cover scheme in Update, got %v", err)
	}
	if _, err := uc.Update(context.Background(), "u1", "p1", "Title", "City", "public", "", -5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for negative budget in Update, got %v", err)
	}
	if _, err := uc.AddItem(context.Background(), "u1", "p1", 1, "scene", strings.Repeat("a", 37), "09:00", "10:00", ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for oversized item_id, got %v", err)
	}
	if _, err := uc.AddItem(context.Background(), "u1", "p1", 1, "scene", "item-1", "09:00", "10:00", strings.Repeat("n", 1001)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for oversized note, got %v", err)
	}
	if _, err := uc.AddItem(context.Background(), "u1", "p1", 1, "scene", "  item-123  ", "09:00", "10:00", "ok"); err != nil {
		t.Fatalf("unexpected error on valid AddItem: %v", err)
	}
	if capturedItem == nil || capturedItem.ItemID != "item-123" {
		t.Fatalf("expected trimmed item_id 'item-123', got %+v", capturedItem)
	}
	if err := CheckItemLimits(MaxItemsPerDay, 10); !errors.Is(err, ErrTooManyItems) {
		t.Fatalf("expected ErrTooManyItems when day count hits limit, got %v", err)
	}
	if err := CheckItemLimits(5, MaxItemsPerPlan); !errors.Is(err, ErrTooManyItems) {
		t.Fatalf("expected ErrTooManyItems when plan count hits limit, got %v", err)
	}
	if err := CheckItemLimits(MaxItemsPerDay-1, MaxItemsPerPlan-1); err != nil {
		t.Fatalf("expected nil below limits, got %v", err)
	}
}

func TestPlanUsecase_PartialUpdateAndPublishedVisibilityToggle(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)

	applyInMemory := func(existing *Plan, patch *Plan) (*Plan, error) {
		fields, err := BuildUpdateFields(existing, patch)
		if err != nil {
			return nil, err
		}
		next := *existing
		if v, ok := fields["title"].(string); ok {
			next.Title = v
		}
		if v, ok := fields["city"].(string); ok {
			next.City = v
		}
		if v, ok := fields["cover"].(string); ok {
			next.Cover = v
		}
		if v, ok := fields["visibility"].(string); ok {
			next.Visibility = v
		}
		if v, ok := fields["budget"].(int); ok {
			next.Budget = v
		}
		return &next, nil
	}

	t.Run("draft partial update preserves omitted city, cover, visibility, and budget", func(t *testing.T) {
		stored := &Plan{
			ID:         "plan-draft-1",
			UID:        "owner-1",
			Title:      "Original Title",
			City:       "Chengdu",
			Cover:      "https://cdn.example.com/chengdu.jpg",
			Budget:     2500,
			Visibility: "private",
			Status:     "draft",
		}
		uc := NewPlanUsecase(&fakePlanRepo{
			updateFn: func(_ context.Context, patch *Plan) (*Plan, error) {
				next, err := applyInMemory(stored, patch)
				if err != nil {
					return nil, err
				}
				stored = next
				return next, nil
			},
		}, logger)

		// Update only title; city, cover, visibility, and budget are omitted ("" / 0).
		updated, err := uc.Update(context.Background(), "owner-1", stored.ID, "Renamed Title", "", "", "", 0)
		if err != nil {
			t.Fatalf("unexpected error on partial title update: %v", err)
		}
		if updated.Title != "Renamed Title" {
			t.Fatalf("expected updated title, got %q", updated.Title)
		}
		if updated.City != "Chengdu" {
			t.Fatalf("expected city preserved as Chengdu, got %q", updated.City)
		}
		if updated.Cover != "https://cdn.example.com/chengdu.jpg" {
			t.Fatalf("expected cover preserved, got %q", updated.Cover)
		}
		if updated.Budget != 2500 {
			t.Fatalf("expected budget preserved as 2500, got %d", updated.Budget)
		}
		if updated.Visibility != "private" {
			t.Fatalf("expected visibility preserved as private, got %q", updated.Visibility)
		}

		// Update only city; title and cover remain preserved.
		updated2, err := uc.Update(context.Background(), "owner-1", stored.ID, "", "Kyoto", "", "", 0)
		if err != nil {
			t.Fatalf("unexpected error on partial city update: %v", err)
		}
		if updated2.Title != "Renamed Title" || updated2.City != "Kyoto" || updated2.Cover != "https://cdn.example.com/chengdu.jpg" {
			t.Fatalf("unexpected state after partial city update: %+v", updated2)
		}
	})

	t.Run("published plan can toggle visibility public to private without wiping metadata", func(t *testing.T) {
		stored := &Plan{
			ID:         "plan-pub-1",
			UID:        "owner-1",
			Title:      "Published Trip",
			City:       "Chengdu",
			Cover:      "https://cdn.example.com/pub.jpg",
			Budget:     3000,
			Visibility: "public",
			Status:     "published",
		}
		uc := NewPlanUsecase(&fakePlanRepo{
			updateFn: func(_ context.Context, patch *Plan) (*Plan, error) {
				next, err := applyInMemory(stored, patch)
				if err != nil {
					return nil, err
				}
				stored = next
				return next, nil
			},
		}, logger)

		// Toggle public -> private with omitted title/city/cover/budget.
		updated, err := uc.Update(context.Background(), "owner-1", stored.ID, "", "", "private", "", 0)
		if err != nil {
			t.Fatalf("expected published visibility toggle to succeed, got %v", err)
		}
		if updated.Visibility != "private" {
			t.Fatalf("expected visibility private, got %q", updated.Visibility)
		}
		if updated.Title != "Published Trip" || updated.City != "Chengdu" || updated.Cover != "https://cdn.example.com/pub.jpg" || updated.Budget != 3000 {
			t.Fatalf("published plan metadata was unexpectedly modified: %+v", updated)
		}

		// Attempting to change title on published plan is rejected with ErrPlanNotEditable.
		if _, err := uc.Update(context.Background(), "owner-1", stored.ID, "Tampered Title", "", "public", "", 0); !errors.Is(err, ErrPlanNotEditable) {
			t.Fatalf("expected ErrPlanNotEditable when changing published title, got %v", err)
		}

		// Attempting to change budget on published plan is rejected with ErrPlanNotEditable.
		if _, err := uc.Update(context.Background(), "owner-1", stored.ID, "", "", "public", "", 9999); !errors.Is(err, ErrPlanNotEditable) {
			t.Fatalf("expected ErrPlanNotEditable when changing published budget, got %v", err)
		}
	})

	t.Run("completely empty update is rejected with ErrInvalidArgument", func(t *testing.T) {
		uc := NewPlanUsecase(&fakePlanRepo{}, logger)
		if _, err := uc.Update(context.Background(), "owner-1", "p1", "   ", "", "", "", 0); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("expected ErrInvalidArgument for empty update, got %v", err)
		}
	})
}
