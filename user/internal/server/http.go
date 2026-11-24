package server

import (
	"time"

	"github.com/knoci/roaming-world/pkg/health"
	v1 "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/conf"
	"github.com/knoci/roaming-world/user/internal/service"

	"github.com/go-kratos/kratos/v2/log"
	mmd "github.com/go-kratos/kratos/v2/middleware/metadata"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/validate"
	"github.com/go-kratos/kratos/v2/transport/http"
)

// NewHTTPServer new an HTTP server.
func NewHTTPServer(c *conf.Server, user *service.UserService, checks []health.Check, logger log.Logger) *http.Server {
	var opts = []http.ServerOption{
		http.Filter(metricsFilter),
		http.Middleware(
			recovery.Recovery(),
			mmd.Server(),
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
	srv.Handle("/healthz", health.LivenessHandler())
	srv.Handle("/readyz", health.ReadinessHandlerWithLogger(logger, 3*time.Second, checks...))
	srv.Route("/").GET("/metrics", metricsHandler)
	v1.RegisterUserHTTPServer(srv, user)
	return srv
}
