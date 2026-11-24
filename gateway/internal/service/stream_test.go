package service

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	pb "github.com/knoci/roaming-world/gateway/api/ai/v1"
	gwpkg "github.com/knoci/roaming-world/gateway/internal/pkg"
)

func newTestGatewayMux(svc *GatewayService) *khttp.Server {
	srv := khttp.NewServer()
	srv.Route("/").POST("/v1/ai/stream", func(ctx khttp.Context) error {
		svc.HandleStream(ctx.Response(), ctx.Request())
		return nil
	})
	srv.Route("/").POST("/v1/ai/threads/{thread_id}/resume", func(ctx khttp.Context) error {
		svc.HandleResumeWithThreadID(ctx.Response(), ctx.Request(), ctx.Vars().Get("thread_id"))
		return nil
	})
	pb.RegisterAiHTTPServer(srv, svc)
	return srv
}

func TestHandleStream_FlushesFramesIncrementallyAndUpdatesTokenQuota(t *testing.T) {
	setupGatewayJWTKey(t)
	logger := log.NewStdLogger(io.Discard)

	frame1Received := make(chan struct{})
	var (
		gotToken       string
		gotUserID      string
		gotUserRole    string
		gotRequestID   string
		gotTraceparent string
		gotAccept      string
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-AI-Gateway-Token")
		gotUserID = r.Header.Get("X-User-ID")
		gotUserRole = r.Header.Get("X-User-Role")
		gotRequestID = r.Header.Get("X-Request-ID")
		gotTraceparent = r.Header.Get("traceparent")
		gotAccept = r.Header.Get("Accept")

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-AI-Tokens-Used", "40")
		w.WriteHeader(http.StatusOK)

		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"delta\":\"hello\"}\n\n"))
		flusher.Flush()

		// Block until client proves it received frame 1 without buffering!
		select {
		case <-frame1Received:
		case <-time.After(3 * time.Second):
			t.Errorf("timed out waiting for client to receive frame 1 incrementally")
			return
		}

		_, _ = w.Write([]byte("data: {\"delta\":\"world\"}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()

	rl := gwpkg.NewMemoryRateLimiter(30, 100)
	svc := NewGatewayServiceWithOptions(
		logger,
		WithHTTPClient(upstream.Client()),
		WithAIEngineURL(upstream.URL),
		WithAIGatewayToken("stream-secret"),
		WithRateLimiter(rl),
	)

	gw := httptest.NewServer(newTestGatewayMux(svc))
	defer gw.Close()

	tok := mintGatewayToken(t, "stream-user-1", "member")
	req, err := http.NewRequest(http.MethodPost, gw.URL+"/v1/ai/stream", strings.NewReader(`{"query":"chengdu 3 days","top_k":3}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Request-ID", "req-sse-1")
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	resp, err := gw.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("expected Cache-Control: no-cache, got %q", resp.Header.Get("Cache-Control"))
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("expected X-Accel-Buffering: no, got %q", resp.Header.Get("X-Accel-Buffering"))
	}

	scanner := bufio.NewScanner(resp.Body)
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, line)
			if len(dataLines) == 1 {
				close(frame1Received)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(dataLines) != 3 {
		t.Fatalf("expected 3 SSE frames, got %d: %v", len(dataLines), dataLines)
	}
	if dataLines[0] != `data: {"delta":"hello"}` || dataLines[1] != `data: {"delta":"world"}` || dataLines[2] != `data: [DONE]` {
		t.Fatalf("unexpected SSE frames: %v", dataLines)
	}
	if gotToken != "stream-secret" || gotUserID != "stream-user-1" || gotUserRole != "member" || gotRequestID != "req-sse-1" || gotAccept != "text/event-stream" {
		t.Fatalf("unexpected forwarded headers: token=%q uid=%q role=%q rid=%q accept=%q", gotToken, gotUserID, gotUserRole, gotRequestID, gotAccept)
	}
	if gotTraceparent != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("unexpected traceparent: %q", gotTraceparent)
	}

	// Verify 40 tokens were deducted from 100 daily budget.
	allowed, rem, err := rl.ConsumeTokens(context.Background(), "stream-user-1", 0)
	if err != nil || !allowed || rem != 60 {
		t.Fatalf("expected remaining 60 tokens, got allowed=%v rem=%d err=%v", allowed, rem, err)
	}
}

func TestHandleStream_ClientDisconnectCancelsUpstreamImmediately(t *testing.T) {
	setupGatewayJWTKey(t)
	logger := log.NewStdLogger(io.Discard)

	upstreamStarted := make(chan struct{})
	upstreamCanceled := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"delta\":\"start\"}\n\n"))
		flusher.Flush()
		close(upstreamStarted)

		<-r.Context().Done()
		close(upstreamCanceled)
	}))
	defer upstream.Close()

	svc := NewGatewayServiceWithOptions(
		logger,
		WithHTTPClient(upstream.Client()),
		WithAIEngineURL(upstream.URL),
		WithAIGatewayToken("stream-secret"),
	)

	gw := httptest.NewServer(newTestGatewayMux(svc))
	defer gw.Close()

	tok := mintGatewayToken(t, "cancel-user", "member")
	reqCtx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, gw.URL+"/v1/ai/stream", strings.NewReader(`{"query":"long stream","top_k":2}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := gw.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}

	select {
	case <-upstreamStarted:
	case <-time.After(3 * time.Second):
		t.Fatalf("upstream did not start")
	}

	// Disconnect client immediately.
	cancel()
	_ = resp.Body.Close()

	select {
	case <-upstreamCanceled:
	case <-time.After(3 * time.Second):
		t.Fatalf("expected upstream request context to be canceled promptly on client disconnect")
	}
}

func TestGateway_RateLimitRPMAndDailyTokenBudget(t *testing.T) {
	setupGatewayJWTKey(t)
	logger := log.NewStdLogger(io.Discard)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-AI-Tokens-Used", "60")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: ok\n\n"))
	}))
	defer upstream.Close()

	t.Run("RPM limit returns 429 with Retry-After", func(t *testing.T) {
		rl := gwpkg.NewMemoryRateLimiter(2, 100000)
		svc := NewGatewayServiceWithOptions(
			logger,
			WithHTTPClient(upstream.Client()),
			WithAIEngineURL(upstream.URL),
			WithAIGatewayToken("tok"),
			WithRateLimiter(rl),
		)
		srv := newTestGatewayMux(svc)
		jwtTok := mintGatewayToken(t, "rpm-user", "member")

		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodPost, "/v1/ai/stream", strings.NewReader(`{"query":"hi","top_k":1}`))
			req.Header.Set("Authorization", "Bearer "+jwtTok)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("request %d expected 200, got %d: %s", i+1, rec.Code, rec.Body.String())
			}
		}

		req := httptest.NewRequest(http.MethodPost, "/v1/ai/stream", strings.NewReader(`{"query":"hi","top_k":1}`))
		req.Header.Set("Authorization", "Bearer "+jwtTok)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 on 3rd request, got %d: %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Fatalf("expected Retry-After header on 429")
		}
	})

	t.Run("daily token budget returns 429 with Retry-After", func(t *testing.T) {
		rl := gwpkg.NewMemoryRateLimiter(30, 100)
		svc := NewGatewayServiceWithOptions(
			logger,
			WithHTTPClient(upstream.Client()),
			WithAIEngineURL(upstream.URL),
			WithAIGatewayToken("tok"),
			WithRateLimiter(rl),
		)
		srv := newTestGatewayMux(svc)
		jwtTok := mintGatewayToken(t, "budget-user", "member")

		// 1st request uses 60 -> 40 left.
		// 2nd request uses 60 -> 120 >= 100 exhausted.
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodPost, "/v1/ai/stream", strings.NewReader(`{"query":"hi","top_k":1}`))
			req.Header.Set("Authorization", "Bearer "+jwtTok)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("request %d expected 200, got %d", i+1, rec.Code)
			}
		}

		// 3rd request must be rejected with 429 + Retry-After before hitting upstream.
		req := httptest.NewRequest(http.MethodPost, "/v1/ai/stream", strings.NewReader(`{"query":"hi","top_k":1}`))
		req.Header.Set("Authorization", "Bearer "+jwtTok)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 when token budget exhausted, got %d: %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Fatalf("expected Retry-After header when token budget exhausted")
		}
	})
}

func TestGateway_CircuitBreakerFastFailsAndRecovers(t *testing.T) {
	setupGatewayJWTKey(t)
	logger := log.NewStdLogger(io.Discard)

	var upstreamCalls atomic.Int32
	var shouldFail atomic.Bool
	shouldFail.Store(true)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if shouldFail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal secret traceback"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: recovered\n\n"))
	}))
	defer upstream.Close()

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	cb := gwpkg.NewCircuitBreaker(2, 30*time.Second).WithClock(func() time.Time { return now })
	svc := NewGatewayServiceWithOptions(
		logger,
		WithHTTPClient(upstream.Client()),
		WithAIEngineURL(upstream.URL),
		WithAIGatewayToken("tok"),
		WithCircuitBreaker(cb),
	)
	srv := newTestGatewayMux(svc)
	jwtTok := mintGatewayToken(t, "cb-user", "member")

	// 2 upstream 500s trip the circuit breaker.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/ai/stream", strings.NewReader(`{"query":"test","top_k":1}`))
		req.Header.Set("Authorization", "Bearer "+jwtTok)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("expected 502 on upstream 500, got %d", rec.Code)
		}
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", upstreamCalls.Load())
	}

	// 3rd request fast-fails with 503 without calling upstream.
	req := httptest.NewRequest(http.MethodPost, "/v1/ai/stream", strings.NewReader(`{"query":"test","top_k":1}`))
	req.Header.Set("Authorization", "Bearer "+jwtTok)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when circuit is open, got %d: %s", rec.Code, rec.Body.String())
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("expected upstream not to be called while circuit is open, got %d calls", upstreamCalls.Load())
	}

	// Advance past cooldown and restore upstream health -> HalfOpen probe succeeds -> Closed.
	now = now.Add(31 * time.Second)
	shouldFail.Store(false)

	req = httptest.NewRequest(http.MethodPost, "/v1/ai/stream", strings.NewReader(`{"query":"test","top_k":1}`))
	req.Header.Set("Authorization", "Bearer "+jwtTok)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after HalfOpen recovery, got %d: %s", rec.Code, rec.Body.String())
	}
	if cb.State() != gwpkg.StateClosed {
		t.Fatalf("expected circuit Closed after recovery, got %v", cb.State())
	}
}

func TestHandleResume_ValidationHeadersAndSanitized5xx(t *testing.T) {
	setupGatewayJWTKey(t)
	logger := log.NewStdLogger(io.Discard)

	var (
		gotPath           string
		gotGatewayToken   string
		gotUserID         string
		gotUserRole       string
		gotRequestID      string
		gotIdempotencyKey string
		gotAuth           string
		return500         atomic.Bool
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotGatewayToken = r.Header.Get("X-AI-Gateway-Token")
		gotUserID = r.Header.Get("X-User-ID")
		gotUserRole = r.Header.Get("X-User-Role")
		gotRequestID = r.Header.Get("X-Request-ID")
		gotIdempotencyKey = r.Header.Get("Idempotency-Key")
		gotAuth = r.Header.Get("Authorization")

		if return500.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"psycopg2.OperationalError: secret_db_password"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"resumed","plan_id":"plan-9"}`))
	}))
	defer upstream.Close()

	svc := NewGatewayServiceWithOptions(
		logger,
		WithHTTPClient(upstream.Client()),
		WithAIEngineURL(upstream.URL),
		WithAIGatewayToken("resume-secret"),
	)
	srv := newTestGatewayMux(svc)
	jwtTok := mintGatewayToken(t, "resume-user-7", "admin")

	t.Run("valid resume forwards headers and returns clean JSON without double-encoding", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/ai/threads/thread_abc-123/resume", strings.NewReader(`{"approved":true}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwtTok)
		req.Header.Set("X-Request-ID", "req-resume-1")
		req.Header.Set("Idempotency-Key", "idem-key-99")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("expected clean JSON body, unmarshal failed: %v (raw=%q)", err, rec.Body.String())
		}
		if out["status"] != "resumed" || out["plan_id"] != "plan-9" {
			t.Fatalf("unexpected response payload (possible double-encoding): %#v", out)
		}
		if gotPath != "/v1/ai/threads/thread_abc-123/resume" {
			t.Fatalf("unexpected upstream path: %q", gotPath)
		}
		if gotGatewayToken != "resume-secret" || gotUserID != "resume-user-7" || gotUserRole != "admin" || gotRequestID != "req-resume-1" || gotIdempotencyKey != "idem-key-99" || gotAuth != "Bearer "+jwtTok {
			t.Fatalf("unexpected forwarded headers: token=%q uid=%q role=%q rid=%q idem=%q auth=%q",
				gotGatewayToken, gotUserID, gotUserRole, gotRequestID, gotIdempotencyKey, gotAuth)
		}
	})

	t.Run("invalid thread_id rejected with 400", func(t *testing.T) {
		for _, badID := range []string{"bad.id", "bad$id", strings.Repeat("a", 65)} {
			req := httptest.NewRequest(http.MethodPost, "/v1/ai/threads/"+badID+"/resume", strings.NewReader(`{"approved":true}`))
			req.Header.Set("Authorization", "Bearer "+jwtTok)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for thread_id %q, got %d: %s", badID, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("upstream 5xx error is sanitized", func(t *testing.T) {
		return500.Store(true)
		defer return500.Store(false)

		req := httptest.NewRequest(http.MethodPost, "/v1/ai/threads/thread_ok_1/resume", strings.NewReader(`{"approved":true}`))
		req.Header.Set("Authorization", "Bearer "+jwtTok)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadGateway {
			t.Fatalf("expected 502 for upstream 500, got %d: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "psycopg2") || strings.Contains(rec.Body.String(), "secret_db_password") {
			t.Fatalf("upstream 5xx leaked sensitive details: %s", rec.Body.String())
		}
	})

	t.Run("missing JWT rejected with 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/ai/threads/thread_ok_1/resume", strings.NewReader(`{"approved":true}`))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})
}
