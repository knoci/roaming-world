package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/knoci/roaming-world/plan/internal/biz"
	"github.com/knoci/roaming-world/plan/internal/conf"
	"github.com/knoci/roaming-world/plan/internal/service"
)

type pingStubRepo struct {
	pingErr error
}

func (r *pingStubRepo) Create(_ context.Context, p *biz.Plan) (*biz.Plan, error) { return p, nil }
func (r *pingStubRepo) FindByID(_ context.Context, _ string) (*biz.Plan, error) {
	return nil, biz.ErrPlanNotFound
}
func (r *pingStubRepo) List(_ context.Context, _, _ string, _, _ int, _ bool) ([]*biz.Plan, int64, error) {
	return nil, 0, nil
}
func (r *pingStubRepo) Update(_ context.Context, p *biz.Plan) (*biz.Plan, error) { return p, nil }
func (r *pingStubRepo) Publish(_ context.Context, _, _ string) error             { return nil }
func (r *pingStubRepo) AddItem(_ context.Context, _ string, _ int, _ *biz.Item) (*biz.Plan, error) {
	return nil, nil
}
func (r *pingStubRepo) DeleteItem(_ context.Context, _, _, _ string) error { return nil }
func (r *pingStubRepo) Delete(_ context.Context, _, _ string) error        { return nil }
func (r *pingStubRepo) Ping(_ context.Context) error                       { return r.pingErr }

func TestPlanHTTPServer_HealthAndReadyz(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)

	t.Run("healthz returns 200", func(t *testing.T) {
		uc := biz.NewPlanUsecase(&pingStubRepo{}, logger)
		srv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, service.NewPlanService(uc, logger), logger)

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/healthz", nil))
		if rec.Code != stdhttp.StatusOK {
			t.Fatalf("expected 200 from /healthz, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("readyz returns 200 when postgres ping succeeds", func(t *testing.T) {
		uc := biz.NewPlanUsecase(&pingStubRepo{pingErr: nil}, logger)
		srv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, service.NewPlanService(uc, logger), logger)

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/readyz", nil))
		if rec.Code != stdhttp.StatusOK {
			t.Fatalf("expected 200 from /readyz, got %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid json body: %v", err)
		}
		if body["status"] != "ready" {
			t.Fatalf("expected status ready, got %v", body["status"])
		}
	})

	t.Run("readyz returns 503 when postgres ping fails", func(t *testing.T) {
		uc := biz.NewPlanUsecase(&pingStubRepo{pingErr: errors.New("connection refused")}, logger)
		srv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, service.NewPlanService(uc, logger), logger)

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/readyz", nil))
		if rec.Code != stdhttp.StatusServiceUnavailable {
			t.Fatalf("expected 503 from /readyz, got %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid json body: %v", err)
		}
		if body["status"] != "not_ready" {
			t.Fatalf("expected status not_ready, got %v", body["status"])
		}
	})
}
