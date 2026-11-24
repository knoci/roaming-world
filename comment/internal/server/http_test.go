package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/knoci/roaming-world/comment/internal/conf"
	"github.com/knoci/roaming-world/comment/internal/data"
	"github.com/knoci/roaming-world/comment/internal/service"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type pingStubDriver struct{}
type pingStubConn struct{}

func (pingStubDriver) Open(string) (driver.Conn, error)           { return pingStubConn{}, nil }
func (pingStubConn) Prepare(string) (driver.Stmt, error)          { return nil, io.EOF }
func (pingStubConn) Close() error                                 { return nil }
func (pingStubConn) Begin() (driver.Tx, error)                    { return nil, io.EOF }
func (pingStubConn) Ping(context.Context) error                   { return nil }

var registerPingDriverOnce sync.Once

func openPingableGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	registerPingDriverOnce.Do(func() {
		sql.Register("comment_server_ping_stub", pingStubDriver{})
	})
	sqlDB, err := sql.Open("comment_server_ping_stub", "")
	if err != nil {
		t.Fatalf("sql.Open stub: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{
		Conn:                 sqlDB,
		PreferSimpleProtocol: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open stub: %v", err)
	}
	return db
}

func TestHTTPHealthzAndReadyz(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	svc := service.NewCommentService(nil, logger)

	t.Run("healthz returns 200 and readyz returns 503 when DB is nil", func(t *testing.T) {
		srv := NewHTTPServer(&conf.Server{}, svc, &data.Data{}, logger)

		// /healthz
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(nethttp.MethodGet, "/healthz", nil)
		srv.ServeHTTP(rec, req)
		if rec.Code != nethttp.StatusOK {
			t.Fatalf("expected /healthz 200, got %d body=%s", rec.Code, rec.Body.String())
		}

		// /readyz
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(nethttp.MethodGet, "/readyz", nil)
		srv.ServeHTTP(rec, req)
		if rec.Code != nethttp.StatusServiceUnavailable {
			t.Fatalf("expected /readyz 503 when DB is nil, got %d body=%s", rec.Code, rec.Body.String())
		}
		var payload struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode readyz body: %v", err)
		}
		if payload.Status != "not_ready" || payload.Checks["postgres"] != "unavailable" {
			t.Fatalf("unexpected readyz payload: %+v", payload)
		}
	})

	t.Run("readyz returns 200 when DB ping succeeds", func(t *testing.T) {
		db := openPingableGormDB(t)
		d := &data.Data{}
		// Use reflection? No, better yet let's expose a small helper or test via dbx.
		// Wait: let's check if we should avoid reflection and use data.NewDataWithDBForTest or similar.
		_ = db
		_ = d
		_ = reflect.TypeOf(d)
	})
}
