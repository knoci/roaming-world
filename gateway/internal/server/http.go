package server

import (
	"context"
	stderrors "errors"
	stdhttp "net/http"
	"os"
	"strings"
	"time"

	v1 "github.com/knoci/roaming-world/gateway/api/ai/v1"
	"github.com/knoci/roaming-world/gateway/internal/conf"
	"github.com/knoci/roaming-world/gateway/internal/service"
	"github.com/knoci/roaming-world/pkg/health"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/validate"
	"github.com/go-kratos/kratos/v2/transport/http"
)

const maxAIBodyBytes = 64 << 10 // 64 KiB

func aiBodyLimitFilter(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/ai/") && r.Body != nil {
			r.Body = stdhttp.MaxBytesReader(w, r.Body, maxAIBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// NewHTTPServer new an HTTP server.
func NewHTTPServer(c *conf.Server, gateway *service.GatewayService, logger log.Logger) *http.Server {
	var opts = []http.ServerOption{
		http.Filter(metricsFilter, aiBodyLimitFilter),
		http.Middleware(
			recovery.Recovery(),
			validate.Validator(),
		),
	}
	if c.Http.Network != "" {
		opts = append(opts, http.Network(c.Http.Network))
	}
	if c.Http.Addr != "" {
		opts = append(opts, http.Address(c.Http.Addr))
	}
	if c.Http.Timeout != nil {
		opts = append(opts, http.Timeout(c.Http.Timeout.AsDuration()))
	}
	srv := http.NewServer(opts...)
	srv.Route("/").GET("/healthz", func(ctx http.Context) error {
		return ctx.String(200, "ok")
	})
	readyHandler := health.ReadinessHandlerWithLogger(logger, 3*time.Second, health.Check{
		Name: "ai_gateway_token",
		Fn: func(context.Context) error {
			if strings.TrimSpace(os.Getenv("AI_GATEWAY_TOKEN")) == "" {
				return stderrors.New("AI_GATEWAY_TOKEN is not configured")
			}
			return nil
		},
	})
	srv.Route("/").GET("/readyz", func(ctx http.Context) error {
		readyHandler(ctx.Response(), ctx.Request())
		return nil
	})
	srv.Route("/").GET("/metrics", metricsHandler)
	srv.Route("/").POST("/v1/ai/stream", func(ctx http.Context) error {
		gateway.HandleStream(ctx.Response(), ctx.Request())
		return nil
	})
	srv.Route("/").POST("/v1/ai/threads/{thread_id}/resume", func(ctx http.Context) error {
		gateway.HandleResumeWithThreadID(ctx.Response(), ctx.Request(), ctx.Vars().Get("thread_id"))
		return nil
	})
	v1.RegisterAiHTTPServer(srv, gateway)
	return srv
}
