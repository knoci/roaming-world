package data

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	pb "github.com/knoci/roaming-world/food/api/food/v1"
	"github.com/knoci/roaming-world/food/internal/biz"
	"github.com/knoci/roaming-world/food/internal/conf"
	"github.com/knoci/roaming-world/food/internal/server"
	"github.com/knoci/roaming-world/food/internal/service"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

type foodQueryTrace struct {
	gormlog.Interface
	randomRows int64
}

func (l *foodQueryTrace) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	sql, rows := query()
	if strings.Contains(sql, "ORDER BY fid") && strings.Contains(sql, "LIMIT 1") {
		l.randomRows = rows
	}
}

func TestFoodCatalogReadsPostgres(t *testing.T) {
	dsn := os.Getenv("ROAMING_TEST_FOOD_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ROAMING_TEST_FOOD_POSTGRES_DSN to an empty, migrated test database")
	}
	trace := &foodQueryTrace{Interface: gormlog.Discard}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: trace})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	logger := log.NewStdLogger(io.Discard)
	uc := biz.NewFoodUsecase(NewFoodRepo(&Data{db: db, log: log.NewHelper(logger)}, logger), logger)
	srv := server.NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, service.NewFoodService(uc, logger), logger)
	get := func(path string, reply proto.Message) {
		t.Helper()
		response := httptest.NewRecorder()
		srv.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body)
		}
		if err := protojson.Unmarshal(response.Body.Bytes(), reply); err != nil {
			t.Fatal(err)
		}
	}
	empty := &pb.GetRandomFoodReply{}
	get("/v1/food/random", empty)
	if empty.Food != nil {
		t.Fatal("empty catalog returned an item")
	}

	old := Food{FID: uuid.NewString(), Name: "Old food", View: []string{"old"}, Location: "Chengdu", CreatedAt: time.Now().Add(-time.Hour)}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&old).Error })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	newFood, err := uc.CreateFood(ctx, &pb.CreateFoodRequest{Name: "New food", View: []string{"new"}, Location: "Chengdu"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Where("fid = ?", newFood.FID).Delete(&Food{}).Error })

	full := &pb.GetFoodListReply{}
	get("/v1/food", full)
	if full.Total != 2 || len(full.Foods) != 2 {
		t.Fatalf("list lost an existing row after creating a food: %v", full)
	}
	seen := []string{full.Foods[0].Fid, full.Foods[1].Fid}
	if !slices.Contains(seen, old.FID) || !slices.Contains(seen, newFood.FID) {
		t.Fatalf("unexpected results: %v", seen)
	}
	first, second, third := &pb.GetFoodListReply{}, &pb.GetFoodListReply{}, &pb.GetFoodListReply{}
	get("/v1/food?page=1&limit=1", first)
	get("/v1/food?page=2&limit=1", second)
	get("/v1/food?page=3&limit=1", third)
	if first.Total != 2 || second.Total != 2 || third.Total != 2 ||
		len(first.Foods) != 1 || first.Foods[0].Fid != newFood.FID ||
		len(second.Foods) != 1 || second.Foods[0].Fid != old.FID || len(third.Foods) != 0 {
		t.Fatalf("unexpected pages: %v %v %v", first, second, third)
	}
	random := &pb.GetRandomFoodReply{}
	get("/v1/food/random", random)
	if random.Food == nil || !slices.Contains(seen, random.Food.Fid) || trace.randomRows != 1 {
		t.Fatalf("random lookup should read exactly one row, got %v and %d rows", random, trace.randomRows)
	}
}
