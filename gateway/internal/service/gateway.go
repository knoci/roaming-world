package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/metadata"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	pb "github.com/knoci/roaming-world/gateway/api/ai/v1"
	"github.com/knoci/roaming-world/gateway/internal/pkg"
)

type GatewayService struct {
	pb.UnimplementedAiServer
	log *log.Helper
}

func NewGatewayService(logger log.Logger) *GatewayService {
	return &GatewayService{log: log.NewHelper(logger)}
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

func (s *GatewayService) proxy(ctx context.Context, path string, req *pb.ChatRequest) (*pb.ProxyReply, error) {
	if _, err := getJwtClaim(ctx); err != nil {
		return nil, errors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	body, _ := json.Marshal(map[string]any{"query": req.Query, "top_k": req.TopK})
	base := strings.TrimRight(os.Getenv("AI_ENGINE_URL"), "/")
	if base == "" {
		base = "http://ai-service:8000"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.InternalServer("AI_PROXY_FAILED", err.Error())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		s.log.WithContext(ctx).Errorf("gateway: ai request failed: %v", err)
		return nil, errors.New(http.StatusBadGateway, "AI_UNAVAILABLE", "ai engine unavailable")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New(resp.StatusCode, "AI_ENGINE_ERROR", string(raw))
	}
	return &pb.ProxyReply{RawJson: string(raw)}, nil
}

func getJwtClaim(ctx context.Context) (*pkg.Claims, error) {
	var authHeader, ua string
	if req, ok := khttp.RequestFromServerContext(ctx); ok && req != nil {
		authHeader = req.Header.Get("Authorization")
		ua = req.Header.Get("User-Agent")
	} else if md, ok := metadata.FromServerContext(ctx); ok {
		authHeader = md.Get("authorization")
		ua = md.Get("user-agent")
	}
	if authHeader == "" {
		return nil, errors.Unauthorized("UNAUTHORIZED", "missing token")
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return nil, errors.Unauthorized("UNAUTHORIZED", "bad token")
	}
	return pkg.ParseToken(ua, parts[1])
}
