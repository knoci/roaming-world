package server

import (
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/knoci/roaming-world/gateway/internal/conf"
	"github.com/knoci/roaming-world/gateway/internal/service"
)

func TestAIBodyLimitFilterRejectsOversizedPayload(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	svc := service.NewGatewayService(logger)
	srv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, svc, logger)

	oversized := `{"query":"` + strings.Repeat("a", maxAIBodyBytes+1024) + `","top_k":5}`
	req := httptest.NewRequest(stdhttp.MethodPost, "/v1/ai/chat", strings.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != stdhttp.StatusBadRequest && rec.Code != stdhttp.StatusRequestEntityTooLarge {
		t.Fatalf("expected 400 or 413 for oversized body, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReadyzEndpoint(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	svc := service.NewGatewayService(logger)
	srv := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, svc, logger)

	t.Run("returns 503 when AI_GATEWAY_TOKEN is not configured", func(t *testing.T) {
		t.Setenv("AI_GATEWAY_TOKEN", "")
		req := httptest.NewRequest(stdhttp.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != stdhttp.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if body.Status != "not_ready" || body.Checks["ai_gateway_token"] != "unavailable" {
			t.Fatalf("unexpected readyz payload: %+v", body)
		}
	})

	t.Run("returns 200 when AI_GATEWAY_TOKEN is configured", func(t *testing.T) {
		t.Setenv("AI_GATEWAY_TOKEN", "secret-gateway-token")
		req := httptest.NewRequest(stdhttp.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != stdhttp.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if body.Status != "ready" || body.Checks["ai_gateway_token"] != "ok" {
			t.Fatalf("unexpected readyz payload: %+v", body)
		}
	})
}
