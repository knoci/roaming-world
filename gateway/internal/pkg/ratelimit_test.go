package pkg

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryRateLimiter_RPMSlidingWindow(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	rl := NewMemoryRateLimiter(3, 1000).WithClock(func() time.Time { return now })
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		allowed, retryAfter, err := rl.AllowRequest(ctx, "user-1")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !allowed || retryAfter != 0 {
			t.Fatalf("request %d should be allowed, got allowed=%v retryAfter=%v", i+1, allowed, retryAfter)
		}
		now = now.Add(10 * time.Second)
	}

	// 4th request within 60s window must be rejected with Retry-After.
	allowed, retryAfter, err := rl.AllowRequest(ctx, "user-1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if allowed {
		t.Fatalf("expected 4th request to be rate-limited")
	}
	if retryAfter < 25*time.Second || retryAfter > 35*time.Second {
		t.Fatalf("expected retryAfter around 30s, got %v", retryAfter)
	}

	// Different user has independent quota.
	allowed, _, err = rl.AllowRequest(ctx, "user-2")
	if err != nil || !allowed {
		t.Fatalf("expected user-2 to be allowed, got allowed=%v err=%v", allowed, err)
	}

	// Advance past first timestamp's 1-minute window.
	now = now.Add(31 * time.Second)
	allowed, retryAfter, err = rl.AllowRequest(ctx, "user-1")
	if err != nil || !allowed || retryAfter != 0 {
		t.Fatalf("expected user-1 to recover after window slide, got allowed=%v retryAfter=%v err=%v", allowed, retryAfter, err)
	}
}

func TestMemoryRateLimiter_DailyTokenBudget(t *testing.T) {
	now := time.Date(2026, 11, 24, 23, 50, 0, 0, time.UTC)
	rl := NewMemoryRateLimiter(30, 100).WithClock(func() time.Time { return now })
	ctx := context.Background()

	// Pre-flight check with 0 tokens.
	allowed, rem, err := rl.ConsumeTokens(ctx, "u1", 0)
	if err != nil || !allowed || rem != 100 {
		t.Fatalf("expected (true, 100, nil), got (%v, %d, %v)", allowed, rem, err)
	}

	// Consume 60 tokens.
	allowed, rem, err = rl.ConsumeTokens(ctx, "u1", 60)
	if err != nil || !allowed || rem != 40 {
		t.Fatalf("expected (true, 40, nil), got (%v, %d, %v)", allowed, rem, err)
	}

	// Consume 40 more -> hits exact budget (allowed, 0 remaining).
	allowed, rem, err = rl.ConsumeTokens(ctx, "u1", 40)
	if err != nil || !allowed || rem != 0 {
		t.Fatalf("expected (true, 0, nil), got (%v, %d, %v)", allowed, rem, err)
	}

	// Pre-flight check now fails.
	allowed, rem, err = rl.ConsumeTokens(ctx, "u1", 0)
	if err != nil || allowed || rem != 0 {
		t.Fatalf("expected (false, 0, nil) when exhausted, got (%v, %d, %v)", allowed, rem, err)
	}

	// Next UTC day resets budget.
	now = now.Add(15 * time.Minute) // 2026-11-25 00:05 UTC
	allowed, rem, err = rl.ConsumeTokens(ctx, "u1", 0)
	if err != nil || !allowed || rem != 100 {
		t.Fatalf("expected daily reset (true, 100, nil), got (%v, %d, %v)", allowed, rem, err)
	}
}

func TestMemoryRateLimiter_ConcurrentSafety(t *testing.T) {
	rl := NewMemoryRateLimiter(50, 1000)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, _, _ = rl.AllowRequest(ctx, "concurrent-user")
				_, _, _ = rl.ConsumeTokens(ctx, "concurrent-user", 5)
			}
		}()
	}
	wg.Wait()
}

func TestRateLimiterEnvParsing(t *testing.T) {
	t.Setenv("GATEWAY_RPM_LIMIT", "45")
	t.Setenv("GATEWAY_DAILY_TOKEN_BUDGET", "250000")
	if got := RPMLimitFromEnv(); got != 45 {
		t.Fatalf("expected 45, got %d", got)
	}
	if got := DailyTokenBudgetFromEnv(); got != 250000 {
		t.Fatalf("expected 250000, got %d", got)
	}

	t.Setenv("GATEWAY_RPM_LIMIT", "invalid")
	t.Setenv("GATEWAY_DAILY_TOKEN_BUDGET", "-5")
	if got := RPMLimitFromEnv(); got != DefaultRPMLimit {
		t.Fatalf("expected default %d, got %d", DefaultRPMLimit, got)
	}
	if got := DailyTokenBudgetFromEnv(); got != DefaultDailyTokenBudget {
		t.Fatalf("expected default %d, got %d", DefaultDailyTokenBudget, got)
	}
}
