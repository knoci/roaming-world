package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

const (
	defaultReadinessTimeout = 3 * time.Second
	checkStatusOK           = "ok"
	checkStatusUnavailable  = "unavailable"
)

// Check represents a single dependency readiness check.
type Check struct {
	Name string
	Fn   func(context.Context) error
}

type livenessResponse struct {
	Status string `json:"status"`
}

type readinessResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// LivenessHandler returns an HTTP handler responding with 200 {"status":"ok"}.
func LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, livenessResponse{Status: checkStatusOK})
	}
}

// ReadinessHandler runs all dependency checks concurrently within timeout and responds with
// 200 {"status":"ready","checks":{...}} when all succeed, or 503 {"status":"not_ready","checks":{...}}
// when any check fails, panics, or times out. Raw internal errors are never exposed to clients.
func ReadinessHandler(timeout time.Duration, checks ...Check) http.HandlerFunc {
	return ReadinessHandlerWithLogger(nil, timeout, checks...)
}

// ReadinessHandlerWithLogger behaves identically to ReadinessHandler while logging
// internal check failures and panics to logger when logger is non-nil.
func ReadinessHandlerWithLogger(logger log.Logger, timeout time.Duration, checks ...Check) http.HandlerFunc {
	if timeout <= 0 {
		timeout = defaultReadinessTimeout
	}
	copied := append([]Check(nil), checks...)

	var helper *log.Helper
	if logger != nil {
		helper = log.NewHelper(logger)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		type outcome struct {
			name string
			ok   bool
			err  error
		}

		outcomes := make([]outcome, len(copied))
		var wg sync.WaitGroup
		wg.Add(len(copied))

		for i, chk := range copied {
			name := strings.TrimSpace(chk.Name)
			if name == "" {
				name = fmt.Sprintf("check_%d", i)
			}
			fn := chk.Fn
			go func(idx int, checkName string, checkFn func(context.Context) error) {
				defer wg.Done()
				err := runCheckWithContext(ctx, checkFn)
				outcomes[idx] = outcome{
					name: checkName,
					ok:   err == nil,
					err:  err,
				}
			}(i, name, fn)
		}

		wg.Wait()

		results := make(map[string]string, len(outcomes))
		allOK := true
		for _, out := range outcomes {
			if out.ok {
				results[out.name] = checkStatusOK
				continue
			}
			allOK = false
			results[out.name] = checkStatusUnavailable
			if helper != nil {
				helper.Warnf("readiness check %q failed: %v", out.name, out.err)
			}
		}

		if allOK {
			writeJSON(w, http.StatusOK, readinessResponse{
				Status: "ready",
				Checks: results,
			})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, readinessResponse{
			Status: "not_ready",
			Checks: results,
		})
	}
}

func runCheckWithContext(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("health check function is nil")
	}

	done := make(chan error, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				done <- fmt.Errorf("panic: %v", rec)
			}
		}()
		done <- fn(ctx)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}
