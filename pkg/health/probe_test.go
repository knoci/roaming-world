package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

func TestLivenessHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	LivenessHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected json content type, got %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestReadinessHandlerAllReadyAndConcurrent(t *testing.T) {
	makeSleepCheck := func(name string) Check {
		return Check{
			Name: name,
			Fn: func(ctx context.Context) error {
				select {
				case <-time.After(40 * time.Millisecond):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		}
	}

	h := ReadinessHandler(
		time.Second,
		makeSleepCheck("postgres"),
		makeSleepCheck("redis"),
		makeSleepCheck("nacos"),
	)

	start := time.Now()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	// 3 checks sleeping 40ms each run concurrently, so total should be well below sequential 120ms.
	if elapsed >= 110*time.Millisecond {
		t.Fatalf("checks appear to have run sequentially: elapsed=%v", elapsed)
	}

	var resp readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.Status != "ready" {
		t.Fatalf("expected status 'ready', got %q", resp.Status)
	}
	for _, k := range []string{"postgres", "redis", "nacos"} {
		if resp.Checks[k] != "ok" {
			t.Fatalf("expected check %q='ok', got %q", k, resp.Checks[k])
		}
	}
}

func TestReadinessHandlerZeroChecks(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	ReadinessHandler(time.Second).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.Status != "ready" || resp.Checks == nil || len(resp.Checks) != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

type captureLogger struct {
	mu      sync.Mutex
	entries []string
}

func (l *captureLogger) Log(level log.Level, keyvals ...any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, fmt.Sprintf("%s %v", level.String(), keyvals))
	return nil
}

func TestReadinessHandlerFailureDoesNotLeakInternalError(t *testing.T) {
	const secretErr = "dial tcp 10.96.0.42:5432: password=super-secret-db-pass auth failed"
	logger := &captureLogger{}

	h := ReadinessHandlerWithLogger(
		logger,
		time.Second,
		Check{
			Name: "redis",
			Fn:   func(context.Context) error { return nil },
		},
		Check{
			Name: "postgres",
			Fn:   func(context.Context) error { return errors.New(secretErr) },
		},
		Check{
			Name: "panicky",
			Fn:   func(context.Context) error { panic("internal-panic-secret") },
		},
		Check{
			Name: "nilfn",
			Fn:   nil,
		},
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	rawBody := rec.Body.String()
	if strings.Contains(rawBody, "super-secret-db-pass") || strings.Contains(rawBody, "internal-panic-secret") {
		t.Fatalf("internal error leaked into HTTP response: %s", rawBody)
	}

	var resp readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.Status != "not_ready" {
		t.Fatalf("expected status 'not_ready', got %q", resp.Status)
	}
	if resp.Checks["redis"] != "ok" ||
		resp.Checks["postgres"] != "unavailable" ||
		resp.Checks["panicky"] != "unavailable" ||
		resp.Checks["nilfn"] != "unavailable" {
		t.Fatalf("unexpected checks map: %+v", resp.Checks)
	}

	// Internal error should have been logged via optional logger.
	logger.mu.Lock()
	joined := strings.Join(logger.entries, "\n")
	logger.mu.Unlock()
	if !strings.Contains(joined, "super-secret-db-pass") {
		t.Fatalf("expected internal error to be logged server-side, got: %s", joined)
	}
}

func TestReadinessHandlerTimeout(t *testing.T) {
	unblock := make(chan struct{})
	defer close(unblock)

	h := ReadinessHandler(
		60*time.Millisecond,
		Check{
			Name: "fast",
			Fn:   func(context.Context) error { return nil },
		},
		Check{
			Name: "ctx-aware-slow",
			Fn: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
		Check{
			Name: "ctx-ignoring-slow",
			Fn: func(context.Context) error {
				<-unblock
				return nil
			},
		},
	)

	start := time.Now()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on timeout, got %d", rec.Code)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("handler did not return promptly on timeout: elapsed=%v", elapsed)
	}

	var resp readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.Checks["fast"] != "ok" ||
		resp.Checks["ctx-aware-slow"] != "unavailable" ||
		resp.Checks["ctx-ignoring-slow"] != "unavailable" {
		t.Fatalf("unexpected checks on timeout: %+v", resp.Checks)
	}
}
