package server

import (
	"context"
	"errors"
	"time"

	"github.com/knoci/roaming-world/pkg/health"
	v1 "github.com/knoci/roaming-world/plan/api/plan/v1"
	"github.com/knoci/roaming-world/plan/internal/conf"
	"github.com/knoci/roaming-world/plan/internal/service"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/validate"
	"github.com/go-kratos/kratos/v2/transport/http"
)

const readinessTimeout = 3 * time.Second

// NewHTTPServer new an HTTP server.
func NewHTTPServer(c *conf.Server, plan *service.PlanService, logger log.Logger) *http.Server {
	var opts = []http.ServerOption{
		http.Filter(metricsFilter),
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
	livenessHandler := health.LivenessHandler()
	readinessHandler := health.ReadinessHandlerWithLogger(
		logger,
		readinessTimeout,
		health.Check{
			Name: "postgres",
			Fn: func(ctx context.Context) error {
				if plan == nil {
					return errors.New("plan service is not initialized")
				}
				return plan.ReadyCheck(ctx)
			},
		},
	)
	srv.Route("/").GET("/healthz", func(ctx http.Context) error {
		livenessHandler(ctx.Response(), ctx.Request())
		return nil
	})
	srv.Route("/").GET("/readyz", func(ctx http.Context) error {
		readinessHandler(ctx.Response(), ctx.Request())
		return nil
	})
	srv.Route("/").GET("/metrics", metricsHandler)
	v1.RegisterPlanHTTPServer(srv, plan)
	return srv
}
