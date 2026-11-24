package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/uuid"
	pb "github.com/knoci/roaming-world/gateway/api/ai/v1"
	gwpkg "github.com/knoci/roaming-world/gateway/internal/pkg"
	"github.com/knoci/roaming-world/pkg/auth"
	grpcmd "google.golang.org/grpc/metadata"
)

const (
	defaultAIEngineURL = "http://ai-service:8000"
	maxAIQueryRunes    = 2000
	defaultTopK        = 5
	minTopK            = 1
	maxTopK            = 10
	maxAIResponseBytes = 2 << 20 // 2 MiB
	maxErrorLogBytes   = 1024
)

// GatewayOption configures a GatewayService (primarily for testing or custom wiring).
type GatewayOption func(*GatewayService)

// WithHTTPClient injects a custom HTTP client used to call the AI engine.
func WithHTTPClient(client *http.Client) GatewayOption {
	return func(s *GatewayService) {
		if client != nil {
			s.httpClient = client
		}
	}
}

// WithAIEngineURL overrides the upstream AI engine base URL.
func WithAIEngineURL(baseURL string) GatewayOption {
	return func(s *GatewayService) {
		s.aiBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		s.baseURLSet = true
	}
}

// WithAIGatewayToken overrides the X-AI-Gateway-Token sent to the AI engine.
func WithAIGatewayToken(token string) GatewayOption {
	return func(s *GatewayService) {
		s.aiToken = token
		s.tokenSet = true
	}
}

// WithRateLimiter injects a custom RateLimiter implementation.
func WithRateLimiter(rl gwpkg.RateLimiter) GatewayOption {
	return func(s *GatewayService) {
		if rl != nil {
			s.rateLimiter = rl
		}
	}
}

// WithCircuitBreaker injects a custom CircuitBreaker instance.
func WithCircuitBreaker(cb *gwpkg.CircuitBreaker) GatewayOption {
	return func(s *GatewayService) {
		if cb != nil {
			s.breaker = cb
		}
	}
}

type GatewayService struct {
	pb.UnimplementedAiServer
	log         *log.Helper
	httpClient  *http.Client
	aiBaseURL   string
	aiToken     string
	baseURLSet  bool
	tokenSet    bool
	rateLimiter gwpkg.RateLimiter
	breaker     *gwpkg.CircuitBreaker
}

func newDefaultHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 50
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 120 * time.Second
	return &http.Client{
		Timeout:   300 * time.Second,
		Transport: transport,
	}
}

func NewGatewayService(logger log.Logger) *GatewayService {
	return NewGatewayServiceWithOptions(logger)
}

func NewGatewayServiceWithOptions(logger log.Logger, opts ...GatewayOption) *GatewayService {
	s := &GatewayService{
		log:         log.NewHelper(logger),
		httpClient:  newDefaultHTTPClient(),
		aiBaseURL:   strings.TrimRight(strings.TrimSpace(os.Getenv("AI_ENGINE_URL")), "/"),
		aiToken:     os.Getenv("AI_GATEWAY_TOKEN"),
		rateLimiter: gwpkg.NewRateLimiterFromEnv(),
		breaker:     gwpkg.NewCircuitBreaker(gwpkg.DefaultBreakerFailureThreshold, gwpkg.DefaultBreakerOpenTimeout),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.httpClient == nil {
		s.httpClient = newDefaultHTTPClient()
	}
	if s.rateLimiter == nil {
		s.rateLimiter = gwpkg.NewMemoryRateLimiter(gwpkg.DefaultRPMLimit, gwpkg.DefaultDailyTokenBudget)
	}
	if s.breaker == nil {
		s.breaker = gwpkg.NewCircuitBreaker(gwpkg.DefaultBreakerFailureThreshold, gwpkg.DefaultBreakerOpenTimeout)
	}
	return s
}

func (s *GatewayService) Chat(ctx context.Context, req *pb.ChatRequest) (*pb.ProxyReply, error) {
	return s.proxy(ctx, "/v1/ai/chat", req)
}

func (s *GatewayService) Agent(ctx context.Context, req *pb.ChatRequest) (*pb.ProxyReply, error) {
	return s.proxy(ctx, "/v1/ai/agent", req)
}

func (s *GatewayService) Graph(ctx context.Context, req *pb.ChatRequest) (*pb.ProxyReply, error) {
	return s.proxy(ctx, "/v1/ai/graph", req)
}

func (s *GatewayService) Multi(ctx context.Context, req *pb.ChatRequest) (*pb.ProxyReply, error) {
	return s.proxy(ctx, "/v1/ai/multi", req)
}

func (s *GatewayService) resolveToken() string {
	if s.tokenSet || s.aiToken != "" {
		return s.aiToken
	}
	return os.Getenv("AI_GATEWAY_TOKEN")
}

func (s *GatewayService) resolveBaseURL() string {
	base := s.aiBaseURL
	if !s.baseURLSet && base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("AI_ENGINE_URL")), "/")
	}
	if base == "" {
		return defaultAIEngineURL
	}
	return base
}

func (s *GatewayService) checkRateLimit(ctx context.Context, uid string) (retryAfter time.Duration, reason string, msg string, ok bool) {
	if s.rateLimiter == nil {
		return 0, "", "", true
	}
	allowed, retry, err := s.rateLimiter.AllowRequest(ctx, uid)
	if err != nil {
		s.log.WithContext(ctx).Warnf("gateway: rate limiter rpm check failed (uid=%s): %v", uid, err)
	} else if !allowed {
		if retry < time.Second {
			retry = time.Second
		}
		return retry, "RATE_LIMITED", "rate limit exceeded", false
	}

	tokAllowed, _, err := s.rateLimiter.ConsumeTokens(ctx, uid, 0)
	if err != nil {
		s.log.WithContext(ctx).Warnf("gateway: rate limiter token check failed (uid=%s): %v", uid, err)
	} else if !tokAllowed {
		return gwpkg.TimeUntilNextUTCDay(time.Now()), "TOKEN_BUDGET_EXCEEDED", "daily token budget exceeded", false
	}
	return 0, "", "", true
}

func (s *GatewayService) recordTokensUsed(ctx context.Context, uid string, headerVal string) {
	if s.rateLimiter == nil {
		return
	}
	raw := strings.TrimSpace(headerVal)
	if raw == "" {
		return
	}
	tokens, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tokens <= 0 {
		return
	}
	if _, _, err := s.rateLimiter.ConsumeTokens(ctx, uid, tokens); err != nil {
		s.log.WithContext(ctx).Warnf("gateway: failed to record token usage (uid=%s, tokens=%d): %v", uid, tokens, err)
	}
}

func retryAfterSeconds(d time.Duration) string {
	secs := int64(math.Ceil(d.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return strconv.FormatInt(secs, 10)
}

func (s *GatewayService) proxy(ctx context.Context, path string, req *pb.ChatRequest) (*pb.ProxyReply, error) {
	claims, err := getJwtClaim(ctx)
	if err != nil {
		return nil, errors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}

	if retryAfter, reason, msg, ok := s.checkRateLimit(ctx, claims.UID); !ok {
		if tr, hasTr := transport.FromServerContext(ctx); hasTr && tr != nil && tr.ReplyHeader() != nil {
			tr.ReplyHeader().Set("Retry-After", retryAfterSeconds(retryAfter))
		}
		return nil, errors.New(http.StatusTooManyRequests, reason, msg)
	}

	if req == nil {
		return nil, errors.BadRequest("INVALID_ARGUMENT", "request is required")
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, errors.BadRequest("INVALID_ARGUMENT", "query must not be empty")
	}
	if utf8.RuneCountInString(req.Query) > maxAIQueryRunes {
		return nil, errors.BadRequest("INVALID_ARGUMENT", "query exceeds maximum length")
	}
	topK := req.TopK
	if topK == 0 {
		topK = defaultTopK
	} else if topK < minTopK || topK > maxTopK {
		return nil, errors.BadRequest("INVALID_ARGUMENT", "top_k must be between 1 and 10")
	}

	token := s.resolveToken()
	if token == "" {
		return nil, errors.New(http.StatusServiceUnavailable, "AI_UNAVAILABLE", "ai engine unavailable")
	}

	if err := s.breaker.Allow(); err != nil {
		return nil, errors.New(http.StatusServiceUnavailable, "AI_CIRCUIT_OPEN", "ai engine temporarily unavailable")
	}

	body, err := json.Marshal(map[string]any{"query": query, "top_k": topK})
	if err != nil {
		s.breaker.CancelProbe()
		s.log.WithContext(ctx).Errorf("gateway: failed to marshal ai request: %v", err)
		return nil, errors.InternalServer("AI_PROXY_FAILED", "failed to prepare ai request")
	}

	base := s.resolveBaseURL()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		s.breaker.CancelProbe()
		s.log.WithContext(ctx).Errorf("gateway: failed to build ai request: %v", err)
		return nil, errors.InternalServer("AI_PROXY_FAILED", "failed to prepare ai request")
	}

	requestID := extractHeader(ctx, "X-Request-ID", "x-request-id")
	if requestID == "" {
		requestID = uuid.NewString()
	}
	traceparent := extractHeader(ctx, "traceparent", "traceparent")

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-AI-Gateway-Token", token)
	httpReq.Header.Set("X-User-ID", claims.UID)
	httpReq.Header.Set("X-User-Role", claims.Role)
	httpReq.Header.Set("X-Request-ID", requestID)
	if traceparent != "" {
		httpReq.Header.Set("traceparent", traceparent)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			s.breaker.CancelProbe()
		} else {
			s.breaker.RecordFailure()
		}
		s.log.WithContext(ctx).Errorf("gateway: ai request failed (request_id=%s): %v", requestID, err)
		return nil, errors.New(http.StatusBadGateway, "AI_UNAVAILABLE", "ai engine unavailable")
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAIResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			s.breaker.CancelProbe()
		} else {
			s.breaker.RecordFailure()
		}
		s.log.WithContext(ctx).Errorf("gateway: failed to read ai response (status=%d, request_id=%s): %v", resp.StatusCode, requestID, err)
		return nil, errors.New(http.StatusBadGateway, "AI_UNAVAILABLE", "ai engine unavailable")
	}
	if len(raw) > maxAIResponseBytes {
		s.breaker.RecordSuccess()
		s.log.WithContext(ctx).Errorf("gateway: ai response too large (status=%d, request_id=%s, limit=%d)", resp.StatusCode, requestID, maxAIResponseBytes)
		return nil, errors.New(http.StatusBadGateway, "AI_RESPONSE_TOO_LARGE", "ai engine response too large")
	}

	if resp.StatusCode >= 500 {
		s.breaker.RecordFailure()
	} else {
		s.breaker.RecordSuccess()
	}
	s.recordTokensUsed(ctx, claims.UID, resp.Header.Get("X-AI-Tokens-Used"))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.log.WithContext(ctx).Errorf("gateway: ai upstream error (status=%d, request_id=%s, body=%q)", resp.StatusCode, requestID, truncateLogBody(raw))
		switch resp.StatusCode {
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			return nil, errors.BadRequest("AI_INVALID_REQUEST", "invalid ai request")
		case http.StatusTooManyRequests:
			return nil, errors.New(http.StatusTooManyRequests, "AI_RATE_LIMITED", "ai service rate limited")
		case http.StatusServiceUnavailable:
			return nil, errors.New(http.StatusServiceUnavailable, "AI_UNAVAILABLE", "ai engine unavailable")
		default:
			return nil, errors.New(http.StatusBadGateway, "AI_UNAVAILABLE", "ai engine unavailable")
		}
	}
	return &pb.ProxyReply{RawJson: string(raw)}, nil
}

func truncateLogBody(raw []byte) string {
	if len(raw) <= maxErrorLogBytes {
		return string(raw)
	}
	return string(raw[:maxErrorLogBytes]) + "...(truncated)"
}

func extractHeader(ctx context.Context, httpKey, mdKey string) string {
	if req, ok := khttp.RequestFromServerContext(ctx); ok && req != nil {
		if v := strings.TrimSpace(req.Header.Get(httpKey)); v != "" {
			return v
		}
	}
	if md, ok := metadata.FromServerContext(ctx); ok {
		if v := strings.TrimSpace(md.Get(mdKey)); v != "" {
			return v
		}
	}
	if gmd, ok := grpcmd.FromIncomingContext(ctx); ok {
		if vals := gmd.Get(mdKey); len(vals) > 0 {
			if v := strings.TrimSpace(vals[0]); v != "" {
				return v
			}
		}
	}
	return ""
}

func getJwtClaim(ctx context.Context) (*auth.Claims, error) {
	claims, err := auth.ParseClaimsFromContext(ctx, nil)
	if err != nil {
		if stderrors.Is(err, auth.ErrMissingToken) {
			return nil, errors.Unauthorized("UNAUTHORIZED", "missing token")
		}
		return nil, errors.Unauthorized("UNAUTHORIZED", "invalid token")
	}
	return claims, nil
}
