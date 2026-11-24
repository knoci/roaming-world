package pkg

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	DefaultRPMLimit         = 30
	DefaultDailyTokenBudget = int64(100000)
	rpmWindowDuration       = time.Minute
)

// RateLimiter enforces per-user RPM and daily token budget limits.
type RateLimiter interface {
	// AllowRequest checks whether uid may issue a request under the per-minute RPM limit.
	// When denied, retryAfter indicates how long the caller should wait before retrying.
	AllowRequest(ctx context.Context, uid string) (allowed bool, retryAfter time.Duration, err error)

	// ConsumeTokens checks (when tokens <= 0) or deducts (when tokens > 0) from uid's daily token budget.
	// Returns whether the user is within budget and the remaining token allowance.
	ConsumeTokens(ctx context.Context, uid string, tokens int64) (allowed bool, remaining int64, err error)
}

// RPMLimitFromEnv parses GATEWAY_RPM_LIMIT or returns DefaultRPMLimit.
func RPMLimitFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("GATEWAY_RPM_LIMIT"))
	if raw == "" {
		return DefaultRPMLimit
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return DefaultRPMLimit
	}
	return v
}

// DailyTokenBudgetFromEnv parses GATEWAY_DAILY_TOKEN_BUDGET or returns DefaultDailyTokenBudget.
func DailyTokenBudgetFromEnv() int64 {
	raw := strings.TrimSpace(os.Getenv("GATEWAY_DAILY_TOKEN_BUDGET"))
	if raw == "" {
		return DefaultDailyTokenBudget
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return DefaultDailyTokenBudget
	}
	return v
}

// NewRateLimiterFromEnv builds a RedisRateLimiter when REDIS_ADDR is configured,
// otherwise defaults to an in-memory MemoryRateLimiter.
func NewRateLimiterFromEnv() RateLimiter {
	rpm := RPMLimitFromEnv()
	budget := DailyTokenBudgetFromEnv()
	if addr := strings.TrimSpace(os.Getenv("REDIS_ADDR")); addr != "" {
		client := redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: os.Getenv("REDIS_PASSWORD"),
		})
		return NewRedisRateLimiter(client, rpm, budget)
	}
	return NewMemoryRateLimiter(rpm, budget)
}

// TimeUntilNextUTCDay returns the duration until the next UTC midnight (minimum 1 second).
func TimeUntilNextUTCDay(now time.Time) time.Duration {
	utc := now.UTC()
	nextDay := time.Date(utc.Year(), utc.Month(), utc.Day()+1, 0, 0, 0, 0, time.UTC)
	d := nextDay.Sub(utc)
	if d < time.Second {
		return time.Second
	}
	return d
}

// MemoryRateLimiter is a thread-safe, zero-dependency in-memory RateLimiter.
type MemoryRateLimiter struct {
	mu          sync.Mutex
	rpmLimit    int
	dailyBudget int64
	window      time.Duration
	now         func() time.Time

	requests map[string][]time.Time
	tokens   map[string]dailyTokenUsage
}

type dailyTokenUsage struct {
	day  string
	used int64
}

// NewMemoryRateLimiter creates a MemoryRateLimiter with the given RPM limit and daily token budget.
func NewMemoryRateLimiter(rpmLimit int, dailyBudget int64) *MemoryRateLimiter {
	if rpmLimit <= 0 {
		rpmLimit = DefaultRPMLimit
	}
	if dailyBudget <= 0 {
		dailyBudget = DefaultDailyTokenBudget
	}
	return &MemoryRateLimiter{
		rpmLimit:    rpmLimit,
		dailyBudget: dailyBudget,
		window:      rpmWindowDuration,
		now:         time.Now,
		requests:    make(map[string][]time.Time),
		tokens:      make(map[string]dailyTokenUsage),
	}
}

// WithClock overrides the clock function (primarily for deterministic testing).
func (m *MemoryRateLimiter) WithClock(now func() time.Time) *MemoryRateLimiter {
	if now != nil {
		m.mu.Lock()
		m.now = now
		m.mu.Unlock()
	}
	return m
}

func (m *MemoryRateLimiter) AllowRequest(_ context.Context, uid string) (bool, time.Duration, error) {
	if uid == "" {
		uid = "anonymous"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	cutoff := now.Add(-m.window)

	ts := m.requests[uid]
	// Prune timestamps outside the sliding window.
	keep := 0
	for _, t := range ts {
		if t.After(cutoff) {
			ts[keep] = t
			keep++
		}
	}
	ts = ts[:keep]

	if len(ts) >= m.rpmLimit {
		m.requests[uid] = ts
		retryAfter := ts[0].Add(m.window).Sub(now)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return false, retryAfter, nil
	}

	ts = append(ts, now)
	m.requests[uid] = ts
	return true, 0, nil
}

func (m *MemoryRateLimiter) ConsumeTokens(_ context.Context, uid string, tokens int64) (bool, int64, error) {
	if uid == "" {
		uid = "anonymous"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	day := m.now().UTC().Format("2006-01-02")
	entry := m.tokens[uid]
	if entry.day != day {
		entry = dailyTokenUsage{day: day, used: 0}
	}

	if tokens <= 0 {
		rem := m.dailyBudget - entry.used
		if rem <= 0 {
			return false, 0, nil
		}
		return true, rem, nil
	}

	if entry.used >= m.dailyBudget {
		m.tokens[uid] = entry
		return false, 0, nil
	}

	entry.used += tokens
	m.tokens[uid] = entry

	if entry.used > m.dailyBudget {
		return false, 0, nil
	}
	return true, m.dailyBudget - entry.used, nil
}

// RedisRateLimiter implements RateLimiter backed by Redis atomic Lua scripts.
type RedisRateLimiter struct {
	client      redis.Cmdable
	rpmLimit    int
	dailyBudget int64
	window      time.Duration
	now         func() time.Time
	seqMu       sync.Mutex
	seq         uint64
}

var (
	// Sliding-window RPM Lua script:
	// KEYS[1]: sorted set key
	// ARGV[1]: now_ms, ARGV[2]: window_ms, ARGV[3]: limit, ARGV[4]: member
	redisRPMLuaScript = redis.NewScript(`
local key = KEYS[1]
local now_ms = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, '-inf', now_ms - window_ms)
local count = redis.call('ZCARD', key)
if count >= limit then
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  local retry_ms = window_ms
  if oldest and #oldest >= 2 then
    retry_ms = (tonumber(oldest[2]) + window_ms) - now_ms
  end
  if retry_ms < 1000 then
    retry_ms = 1000
  end
  return {0, retry_ms}
end

redis.call('ZADD', key, now_ms, member)
redis.call('PEXPIRE', key, window_ms + 1000)
return {1, 0}
`)

	// Daily token budget Lua script:
	// KEYS[1]: counter key
	// ARGV[1]: tokens, ARGV[2]: budget, ARGV[3]: ttl_seconds
	redisTokensLuaScript = redis.NewScript(`
local key = KEYS[1]
local tokens = tonumber(ARGV[1])
local budget = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])

local current = tonumber(redis.call('GET', key) or '0')
if tokens <= 0 then
  local rem = budget - current
  if rem <= 0 then
    return {0, 0}
  end
  return {1, rem}
end

if current >= budget then
  return {0, 0}
end

local next_val = redis.call('INCRBY', key, tokens)
if next_val == tokens then
  redis.call('EXPIRE', key, ttl)
end
if next_val > budget then
  return {0, 0}
end
return {1, budget - next_val}
`)
)

// NewRedisRateLimiter creates a Redis-backed RateLimiter using atomic Lua scripts.
func NewRedisRateLimiter(client redis.Cmdable, rpmLimit int, dailyBudget int64) *RedisRateLimiter {
	if rpmLimit <= 0 {
		rpmLimit = DefaultRPMLimit
	}
	if dailyBudget <= 0 {
		dailyBudget = DefaultDailyTokenBudget
	}
	return &RedisRateLimiter{
		client:      client,
		rpmLimit:    rpmLimit,
		dailyBudget: dailyBudget,
		window:      rpmWindowDuration,
		now:         time.Now,
	}
}

func (r *RedisRateLimiter) nextSeq() uint64 {
	r.seqMu.Lock()
	r.seq++
	s := r.seq
	r.seqMu.Unlock()
	return s
}

func (r *RedisRateLimiter) AllowRequest(ctx context.Context, uid string) (bool, time.Duration, error) {
	if uid == "" {
		uid = "anonymous"
	}
	now := r.now()
	nowMs := now.UnixMilli()
	windowMs := r.window.Milliseconds()
	member := fmt.Sprintf("%d-%d", now.UnixNano(), r.nextSeq())
	key := "gw:rpm:" + uid

	res, err := redisRPMLuaScript.Run(ctx, r.client, []string{key}, nowMs, windowMs, r.rpmLimit, member).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("ratelimit: redis rpm script: %w", err)
	}
	if len(res) < 2 {
		return false, 0, fmt.Errorf("ratelimit: unexpected redis rpm script result")
	}
	if res[0] == 1 {
		return true, 0, nil
	}
	retry := time.Duration(res[1]) * time.Millisecond
	if retry < time.Second {
		retry = time.Second
	}
	return false, retry, nil
}

func (r *RedisRateLimiter) ConsumeTokens(ctx context.Context, uid string, tokens int64) (bool, int64, error) {
	if uid == "" {
		uid = "anonymous"
	}
	now := r.now()
	day := now.UTC().Format("2006-01-02")
	ttl := int64(TimeUntilNextUTCDay(now).Seconds()) + 60
	key := fmt.Sprintf("gw:tok:%s:%s", uid, day)

	res, err := redisTokensLuaScript.Run(ctx, r.client, []string{key}, tokens, r.dailyBudget, ttl).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("ratelimit: redis token script: %w", err)
	}
	if len(res) < 2 {
		return false, 0, fmt.Errorf("ratelimit: unexpected redis token script result")
	}
	return res[0] == 1, res[1], nil
}
