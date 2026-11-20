package data

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	pb "github.com/knoci/roaming-world/plan/api/plan/v1"
	"github.com/knoci/roaming-world/plan/internal/biz"
	"github.com/knoci/roaming-world/plan/internal/conf"
	"github.com/knoci/roaming-world/plan/internal/server"
	"github.com/knoci/roaming-world/plan/internal/service"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

type selectCounter struct {
	gormlog.Interface
	total atomic.Int64
}

func (c *selectCounter) Trace(_ context.Context, _ time.Time, sql func() (string, int64), _ error) {
	query, _ := sql()
	if strings.HasPrefix(query, "SELECT") {
		c.total.Add(1)
	}
}

func TestPlanWorkflowPostgres(t *testing.T) {
	dsn := os.Getenv("ROAMING_TEST_PLAN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ROAMING_TEST_PLAN_POSTGRES_DSN to a migrated test database")
	}
	counter := &selectCounter{Interface: gormlog.Discard}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: counter})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger := log.NewStdLogger(io.Discard)
	uc := biz.NewPlanUsecase(NewPlanRepo(&Data{db: db}, logger), logger)
	owner := uuid.NewString()

	create := func(t *testing.T, visibility string) *biz.Plan {
		t.Helper()
		plan, err := uc.Create(ctx, owner, "Fixture", "Chengdu", "2030-01-01", "2030-01-02", visibility, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := uc.Delete(cleanupCtx, owner, plan.ID); err != nil {
				t.Error(err)
			}
		})
		if len(plan.Days) != 2 || plan.Days[1].DayNo != 2 || plan.CreatedAt.IsZero() {
			t.Fatalf("unexpected created plan: %+v", plan)
		}
		return plan
	}

	t.Run("concurrent overlapping items", func(t *testing.T) {
		plan := create(t, "private")
		add := func(start, end string) error {
			_, err := uc.AddItem(ctx, owner, plan.ID, 1, "scene", "reference", start, end, "")
			return err
		}
		first, second := parallelWrites(
			func() error { return add("09:00", "10:00") },
			func() error { return add("09:30", "10:30") },
		)
		if !((first == nil && errors.Is(second, biz.ErrTimeOverlap)) ||
			(second == nil && errors.Is(first, biz.ErrTimeOverlap))) {
			t.Fatalf("expected one insert and one overlap, got %v and %v", first, second)
		}
		if err := add("08:00", "09:00"); err != nil {
			t.Fatalf("adjacent item was rejected: %v", err)
		}
		current, err := uc.Get(ctx, plan.ID, owner)
		if err != nil {
			t.Fatal(err)
		}
		items := current.Days[0].Items
		if len(items) != 2 || items[0].StartTime != "08:00" {
			t.Fatalf("expected two items in chronological order, got %+v", items)
		}
	})

	t.Run("publish versus deleting the last item", func(t *testing.T) {
		plan := create(t, "public")
		if _, err := uc.Publish(ctx, owner, plan.ID); !errors.Is(err, biz.ErrEmptyPlan) {
			t.Fatalf("empty draft returned %v", err)
		}
		current, err := uc.AddItem(ctx, owner, plan.ID, 1, "scene", "reference", "09:00", "10:00", "")
		if err != nil {
			t.Fatal(err)
		}
		itemID := current.Days[0].Items[0].ID
		publishErr, deleteErr := parallelWrites(
			func() error {
				_, err := uc.Publish(ctx, owner, plan.ID)
				return err
			},
			func() error { return uc.DeleteItem(ctx, owner, plan.ID, itemID) },
		)
		current, err = uc.Get(ctx, plan.ID, owner)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case publishErr == nil && errors.Is(deleteErr, biz.ErrPlanNotEditable):
			if current.Status != "published" || len(current.Days[0].Items) != 1 {
				t.Fatal("published plan lost its last item")
			}
		case deleteErr == nil && errors.Is(publishErr, biz.ErrEmptyPlan):
			if current.Status != "draft" || len(current.Days[0].Items) != 0 {
				t.Fatal("plan changed after deletion prevented publication")
			}
		default:
			t.Fatalf("unexpected race outcome: publish %v, delete %v", publishErr, deleteErr)
		}
	})

	t.Run("HTTP summaries skip child reads", func(t *testing.T) {
		plan := create(t, "public")
		if _, err := uc.Update(ctx, owner, plan.ID, "Updated", "", "", "", 0); err != nil {
			t.Fatal(err)
		}
		if _, err := uc.AddItem(ctx, owner, plan.ID, 1, "food", "reference", "09:00", "10:00", "Breakfast"); err != nil {
			t.Fatal(err)
		}
		if _, err := uc.Publish(ctx, owner, plan.ID); err != nil {
			t.Fatal(err)
		}
		srv := server.NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, service.NewPlanService(uc, logger), logger)
		getList := func(query string) (*pb.PlanMessage, int64, int64) {
			t.Helper()
			counter.total.Store(0)
			response := httptest.NewRecorder()
			srv.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/plan?scope=public"+query, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("list request failed: %d %s", response.Code, response.Body)
			}
			reply := &pb.ListPlansReply{}
			if err := protojson.Unmarshal(response.Body.Bytes(), reply); err != nil {
				t.Fatal(err)
			}
			for _, result := range reply.Plans {
				if result.Id == plan.ID {
					return result, reply.Total, counter.total.Load()
				}
			}
			t.Fatal("published plan missing from the public list")
			return nil, 0, 0
		}
		full, total, fullQueries := getList("")
		summary, summaryTotal, summaryQueries := getList("&summary_only=true")
		if len(full.Days) != 2 || len(full.Days[0].Items) != 1 || len(summary.Days) != 0 ||
			total != summaryTotal || summary.Title != "Updated" {
			t.Fatal("full and summary responses differ from their contract")
		}
		if fullQueries != 4 || summaryQueries != 2 {
			t.Fatalf("expected 4 full queries and 2 summary queries, got %d and %d", fullQueries, summaryQueries)
		}
		t.Logf("list SELECTs: full %d, summary %d", fullQueries, summaryQueries)
	})
}

func parallelWrites(first, second func() error) (error, error) {
	start := make(chan struct{})
	firstResult, secondResult := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		firstResult <- first()
	}()
	go func() {
		<-start
		secondResult <- second()
	}()
	close(start)
	return <-firstResult, <-secondResult
}
