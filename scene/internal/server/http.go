package server

import (
	"time"

	"github.com/knoci/roaming-world/pkg/health"
	v1 "github.com/knoci/roaming-world/scene/api/scene/v1"
	"github.com/knoci/roaming-world/scene/internal/conf"
	"github.com/knoci/roaming-world/scene/internal/service"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/validate"
	"github.com/go-kratos/kratos/v2/transport/http"
)

// NewHTTPServer new an HTTP server.
func NewHTTPServer(c *conf.Server, scene *service.SceneService, logger log.Logger, checks ...health.Check) *http.Server {
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
	liveness := health.LivenessHandler()
	readiness := health.ReadinessHandlerWithLogger(logger, 3*time.Second, checks...)
	srv.Route("/").GET("/healthz", func(ctx http.Context) error {
		liveness(ctx.Response(), ctx.Request())
		return nil
	})
	srv.Route("/").GET("/readyz", func(ctx http.Context) error {
		readiness(ctx.Response(), ctx.Request())
		return nil
	})
	srv.Route("/").GET("/metrics", metricsHandler)
	v1.RegisterSceneHTTPServer(srv, scene)
	return srv
}

// ProvideHTTPServer adapts Wire-injected readiness checks for NewHTTPServer.
func ProvideHTTPServer(c *conf.Server, scene *service.SceneService, logger log.Logger, checks []health.Check) *http.Server {
	return NewHTTPServer(c, scene, logger, checks...)
}
