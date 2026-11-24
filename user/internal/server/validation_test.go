package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/validate"
	"github.com/knoci/roaming-world/pkg/health"
	pb "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/biz"
	"github.com/knoci/roaming-world/user/internal/conf"
	"github.com/knoci/roaming-world/user/internal/service"
)

type noopRepo struct {
	biz.UserRepo
}

func (r *noopRepo) FindByEmail(_ context.Context, _ string) (*biz.User, error) {
	return nil, biz.ErrUserNotFound
}

func (r *noopRepo) UploadAvatar(_ context.Context, _ string, _ *multipart.FileHeader) (*biz.User, error) {
	return nil, biz.ErrUserNotFound
}

func TestProtoValidationMiddleware(t *testing.T) {
	mw := validate.Validator()(func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	})

	// Invalid LoginRequest (bad email) rejected
	if _, err := mw(context.Background(), &pb.LoginRequest{
		Email:    "not-an-email",
		Password: "valid-password-12345",
	}); err == nil || kerrors.Code(err) != 400 {
		t.Fatalf("expected 400 validation error for invalid email, got %v", err)
	}

	// Invalid LoginRequest (password too short) rejected
	if _, err := mw(context.Background(), &pb.LoginRequest{
		Email:    "user@example.com",
		Password: "123",
	}); err == nil || kerrors.Code(err) != 400 {
		t.Fatalf("expected 400 validation error for short password, got %v", err)
	}

	// 20-char password MUST be allowed on LoginRequest (was broken when max_len was 18)
	if _, err := mw(context.Background(), &pb.LoginRequest{
		Email:    "user@example.com",
		Password: strings.Repeat("p", 20),
	}); err != nil {
		t.Fatalf("expected 20-char password login to pass validation, got %v", err)
	}

	// 72-char password allowed, 73-char rejected
	if _, err := mw(context.Background(), &pb.LoginRequest{
		Email:    "user@example.com",
		Password: strings.Repeat("p", 72),
	}); err != nil {
		t.Fatalf("expected 72-char password login to pass validation, got %v", err)
	}
	if _, err := mw(context.Background(), &pb.LoginRequest{
		Email:    "user@example.com",
		Password: strings.Repeat("p", 73),
	}); err == nil {
		t.Fatal("expected 73-char password login to fail validation")
	}

	// RegisterRequest name min_len=1 max_len=20, password max_len=72
	if _, err := mw(context.Background(), &pb.RegisterRequest{
		Name:     "a",
		Email:    "a@example.com",
		Password: strings.Repeat("p", 20),
		Code:     "123456",
	}); err != nil {
		t.Fatalf("expected 1-char name and 20-char password register to pass, got %v", err)
	}
	if _, err := mw(context.Background(), &pb.RegisterRequest{
		Name:     strings.Repeat("n", 21),
		Email:    "a@example.com",
		Password: "password123",
		Code:     "123456",
	}); err == nil {
		t.Fatal("expected 21-char name register to fail validation")
	}
}

func TestHTTPServerValidationMiddlewareAndVerifyPOSTRoute(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	uc := biz.NewUserUsecase(&noopRepo{}, nil, logger)
	svc := service.NewUserService(uc, nil, nil, logger)
	srv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, svc, nil, logger)

	// Invalid login body should be rejected by validate.Validator() with 400
	req := httptest.NewRequest(nethttp.MethodPost, "/v1/user/login", bytes.NewBufferString(`{"email":"bad","password":"123"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusBadRequest {
		t.Fatalf("expected 400 from HTTP validation middleware, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 20-char password login should pass validation and reach Login -> 401 (user not found)
	req = httptest.NewRequest(nethttp.MethodPost, "/v1/user/login", bytes.NewBufferString(`{"email":"alice@example.com","password":"12345678901234567890"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("expected 401 after passing validation with 20-char password, got %d (%s)", rec.Code, rec.Body.String())
	}

	// GET /v1/user/verify should no longer be allowed (changed to POST)
	getVerifyReq := httptest.NewRequest(nethttp.MethodGet, "/v1/user/verify?email=alice@example.com", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, getVerifyReq)
	if rec.Code != nethttp.StatusMethodNotAllowed && rec.Code != nethttp.StatusNotFound {
		t.Fatalf("expected GET /v1/user/verify to be rejected (405/404), got %d", rec.Code)
	}

	// POST /v1/user/verify with invalid email should be caught by validator with 400
	postVerifyReq := httptest.NewRequest(nethttp.MethodPost, "/v1/user/verify", bytes.NewBufferString(`{"email":"invalid-email"}`))
	postVerifyReq.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, postVerifyReq)
	if rec.Code != nethttp.StatusBadRequest {
		t.Fatalf("expected POST /v1/user/verify with invalid email to return 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}


func TestHTTPServerHealthzAndReadyzProbes(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	uc := biz.NewUserUsecase(&noopRepo{}, nil, logger)
	svc := service.NewUserService(uc, nil, nil, logger)

	readySrv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, svc, []health.Check{
		{Name: "postgres", Fn: func(context.Context) error { return nil }},
		{Name: "redis", Fn: func(context.Context) error { return nil }},
	}, logger)

	rec := httptest.NewRecorder()
	readySrv.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/healthz", nil))
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	readySrv.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/readyz", nil))
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("expected 200 on /readyz when all checks pass, got %d (%s)", rec.Code, rec.Body.String())
	}
	var readyBody struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &readyBody); err != nil {
		t.Fatalf("decode readyz json: %v", err)
	}
	if readyBody.Status != "ready" || readyBody.Checks["postgres"] != "ok" || readyBody.Checks["redis"] != "ok" {
		t.Fatalf("unexpected readyz payload: %+v", readyBody)
	}

	failingSrv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, svc, []health.Check{
		{Name: "postgres", Fn: func(context.Context) error { return nil }},
		{Name: "redis", Fn: func(context.Context) error { return errors.New("secret redis password leak") }},
	}, logger)
	rec = httptest.NewRecorder()
	failingSrv.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/readyz", nil))
	if rec.Code != nethttp.StatusServiceUnavailable {
		t.Fatalf("expected 503 on /readyz when redis fails, got %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret redis password leak") {
		t.Fatalf("readyz leaked internal error: %s", rec.Body.String())
	}
}
