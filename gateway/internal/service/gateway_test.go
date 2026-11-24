package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	jwt "github.com/golang-jwt/jwt/v5"
	pb "github.com/knoci/roaming-world/gateway/api/ai/v1"
	"github.com/knoci/roaming-world/pkg/auth"
	grpcmd "google.golang.org/grpc/metadata"
)

func setupGatewayJWTKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 11)
	}
	t.Setenv("JWT_SIGNING_KEY", base64.StdEncoding.EncodeToString(key))
	auth.ResetKeyCacheForTest()
	t.Cleanup(auth.ResetKeyCacheForTest)
	return key
}

func mintGatewayToken(t *testing.T, uid, role string) string {
	t.Helper()
	tok, _, err := auth.GenerateAccessToken(uid, "", "", "", role, 1, time.Hour)
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}
	return tok
}

func TestGatewayService_ForwardsHeadersAndDefaultsTopK(t *testing.T) {
	setupGatewayJWTKey(t)
	logger := log.NewStdLogger(io.Discard)

	var (
		gotGatewayToken string
		gotUserID       string
		gotUserRole     string
		gotRequestID    string
		gotTraceparent  string
		gotPayload      map[string]any
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotGatewayToken = r.Header.Get("X-AI-Gateway-Token")
		gotUserID = r.Header.Get("X-User-ID")
		gotUserRole = r.Header.Get("X-User-Role")
		gotRequestID = r.Header.Get("X-Request-ID")
		gotTraceparent = r.Header.Get("traceparent")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotPayload)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"answer":"hello"}`))
	}))
	defer upstream.Close()

	svc := NewGatewayServiceWithOptions(
		logger,
		WithHTTPClient(upstream.Client()),
		WithAIEngineURL(upstream.URL),
		WithAIGatewayToken("secret-gateway-token"),
	)

	srv := khttp.NewServer()
	pb.RegisterAiHTTPServer(srv, svc)

	tok := mintGatewayToken(t, "uid-42", "admin")

	req := httptest.NewRequest(http.MethodPost, "/v1/ai/chat", strings.NewReader(`{"query":"best food in chengdu","top_k":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotGatewayToken != "secret-gateway-token" {
		t.Fatalf("expected X-AI-Gateway-Token to be forwarded, got %q", gotGatewayToken)
	}
	if gotUserID != "uid-42" {
		t.Fatalf("expected X-User-ID uid-42, got %q", gotUserID)
	}
	if gotUserRole != "admin" {
		t.Fatalf("expected X-User-Role admin, got %q", gotUserRole)
	}
	if gotRequestID == "" {
		t.Fatalf("expected generated X-Request-ID to be non-empty")
	}
	if gotTraceparent != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("expected traceparent forwarded, got %q", gotTraceparent)
	}
	if topK, ok := gotPayload["top_k"].(float64); !ok || topK != 5 {
		t.Fatalf("expected top_k defaulted to 5, got %#v", gotPayload["top_k"])
	}
}

func TestGatewayService_SanitizesUpstreamErrorsAndRejectsOversizedResponse(t *testing.T) {
	setupGatewayJWTKey(t)
	logger := log.NewStdLogger(io.Discard)
	tok := mintGatewayToken(t, "uid-99", "user")
	authCtx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
		"authorization", "Bearer "+tok,
		"x-request-id", "req-fixed-123",
	))

	cases := []struct {
		name           string
		upstreamStatus int
		upstreamBody   string
		wantCode       int32
		wantReason     string
		forbiddenText  string
	}{
		{
			name:           "upstream 500 DB error sanitized",
			upstreamStatus: http.StatusInternalServerError,
			upstreamBody:   `{"detail":"psycopg2.OperationalError: password authentication failed for user postgres"}`,
			wantCode:       http.StatusBadGateway,
			wantReason:     "AI_UNAVAILABLE",
			forbiddenText:  "psycopg2",
		},
		{
			name:           "upstream 503 sanitized",
			upstreamStatus: http.StatusServiceUnavailable,
			upstreamBody:   `internal redis cluster 10.0.0.5:6379 down`,
			wantCode:       http.StatusServiceUnavailable,
			wantReason:     "AI_UNAVAILABLE",
			forbiddenText:  "10.0.0.5",
		},
		{
			name:           "upstream 401 internal token error sanitized",
			upstreamStatus: http.StatusUnauthorized,
			upstreamBody:   `invalid X-AI-Gateway-Token secret-xyz`,
			wantCode:       http.StatusBadGateway,
			wantReason:     "AI_UNAVAILABLE",
			forbiddenText:  "secret-xyz",
		},
		{
			name:           "upstream 422 mapped to 400 safe error",
			upstreamStatus: http.StatusUnprocessableEntity,
			upstreamBody:   `pydantic traceback internal/app.py line 42`,
			wantCode:       http.StatusBadRequest,
			wantReason:     "AI_INVALID_REQUEST",
			forbiddenText:  "traceback",
		},
		{
			name:           "upstream 429 mapped to 429 rate limited",
			upstreamStatus: http.StatusTooManyRequests,
			upstreamBody:   `upstream quota details`,
			wantCode:       http.StatusTooManyRequests,
			wantReason:     "AI_RATE_LIMITED",
			forbiddenText:  "quota details",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Request-ID") != "req-fixed-123" {
					t.Errorf("expected propagated X-Request-ID req-fixed-123, got %q", r.Header.Get("X-Request-ID"))
				}
				w.WriteHeader(tc.upstreamStatus)
				_, _ = w.Write([]byte(tc.upstreamBody))
			}))
			defer upstream.Close()

			svc := NewGatewayServiceWithOptions(
				logger,
				WithHTTPClient(upstream.Client()),
				WithAIEngineURL(upstream.URL),
				WithAIGatewayToken("token-ok"),
			)

			_, err := svc.Chat(authCtx, &pb.ChatRequest{Query: "plan a trip", TopK: 3})
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			ke := kerrors.FromError(err)
			if ke.Code != tc.wantCode {
				t.Fatalf("expected HTTP code %d, got %d (%v)", tc.wantCode, ke.Code, ke)
			}
			if ke.Reason != tc.wantReason {
				t.Fatalf("expected reason %q, got %q", tc.wantReason, ke.Reason)
			}
			if strings.Contains(ke.Message, tc.forbiddenText) || strings.Contains(err.Error(), tc.forbiddenText) {
				t.Fatalf("raw upstream error leaked to client: %v", err)
			}
		})
	}

	t.Run("oversized upstream response rejected cleanly", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", maxAIResponseBytes+10)))
		}))
		defer upstream.Close()

		svc := NewGatewayServiceWithOptions(
			logger,
			WithHTTPClient(upstream.Client()),
			WithAIEngineURL(upstream.URL),
			WithAIGatewayToken("token-ok"),
		)

		_, err := svc.Chat(authCtx, &pb.ChatRequest{Query: "large response", TopK: 3})
		if err == nil {
			t.Fatalf("expected error for oversized response")
		}
		ke := kerrors.FromError(err)
		if ke.Code != http.StatusBadGateway || ke.Reason != "AI_RESPONSE_TOO_LARGE" {
			t.Fatalf("expected 502 AI_RESPONSE_TOO_LARGE, got %d %s", ke.Code, ke.Reason)
		}
	})
}

func TestGatewayService_InputValidation(t *testing.T) {
	setupGatewayJWTKey(t)
	logger := log.NewStdLogger(io.Discard)
	tok := mintGatewayToken(t, "uid-1", "user")
	authCtx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
		"authorization", "Bearer "+tok,
	))

	svc := NewGatewayServiceWithOptions(
		logger,
		WithAIEngineURL("http://127.0.0.1:1"),
		WithAIGatewayToken("token-ok"),
	)

	cases := []struct {
		name string
		req  *pb.ChatRequest
	}{
		{name: "empty query", req: &pb.ChatRequest{Query: "   ", TopK: 5}},
		{name: "query over 2000 runes", req: &pb.ChatRequest{Query: strings.Repeat("旅", 2001), TopK: 5}},
		{name: "negative top_k", req: &pb.ChatRequest{Query: "hello", TopK: -1}},
		{name: "top_k over 10", req: &pb.ChatRequest{Query: "hello", TopK: 11}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Chat(authCtx, tc.req)
			if err == nil {
				t.Fatalf("expected 400 error, got nil")
			}
			if ke := kerrors.FromError(err); ke.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (%v)", ke.Code, ke)
			}
		})
	}
}

func TestGatewayService_GRPCAuthAndEmptyUIDRejected(t *testing.T) {
	key := setupGatewayJWTKey(t)
	logger := log.NewStdLogger(io.Discard)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	svc := NewGatewayServiceWithOptions(
		logger,
		WithHTTPClient(upstream.Client()),
		WithAIEngineURL(upstream.URL),
		WithAIGatewayToken("token-ok"),
	)

	t.Run("gRPC incoming metadata auth succeeds", func(t *testing.T) {
		tok := mintGatewayToken(t, "grpc-user-7", "member")
		ctx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
			"authorization", "Bearer "+tok,
		))
		reply, err := svc.Agent(ctx, &pb.ChatRequest{Query: "hi", TopK: 2})
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		if reply.GetRawJson() != `{"ok":true}` {
			t.Fatalf("unexpected reply: %q", reply.GetRawJson())
		}
	})

	t.Run("gRPC incoming metadata with empty UID rejected with 401", func(t *testing.T) {
		tokObj := jwt.NewWithClaims(jwt.SigningMethodHS256, &auth.Claims{
			UID:  "",
			Role: "member",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    auth.DefaultIssuer,
				Audience:  jwt.ClaimStrings{auth.DefaultAudience},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		})
		tok, err := tokObj.SignedString(key)
		if err != nil {
			t.Fatalf("sign token: %v", err)
		}
		ctx := grpcmd.NewIncomingContext(context.Background(), grpcmd.Pairs(
			"authorization", "Bearer "+tok,
		))
		_, err = svc.Chat(ctx, &pb.ChatRequest{Query: "hi", TopK: 2})
		if err == nil {
			t.Fatalf("expected 401 error for empty UID")
		}
		if ke := kerrors.FromError(err); ke.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d (%v)", ke.Code, ke)
		}
	})
}
