package server

import (
	"time"

	"github.com/knoci/roaming-world/comment/internal/conf"
	"github.com/knoci/roaming-world/comment/internal/data"
	"github.com/knoci/roaming-world/comment/internal/service"
	"github.com/knoci/roaming-world/pkg/health"

	"github.com/go-kratos/kratos/v2/log"
	mmd "github.com/go-kratos/kratos/v2/middleware/metadata"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/validate"
	"github.com/go-kratos/kratos/v2/transport/http"
	v1 "github.com/knoci/roaming-world/comment/api/comment/v1"
)

// NewHTTPServer new an HTTP server.
func NewHTTPServer(c *conf.Server, comment *service.CommentService, d *data.Data, logger log.Logger) *http.Server {
	var opts = []http.ServerOption{
		http.Filter(metricsFilter),
		http.Middleware(
			recovery.Recovery(),
			mmd.Server(mmd.WithPropagatedPrefix("")),
			validate.Validator(),
		),
	}
	if c != nil && c.Http != nil {
		if c.Http.Network != "" {
			opts = append(opts, http.Network(c.Http.Network))
		}
		if c.Http.Addr != "" {
			opts = append(opts, http.Address(c.Http.Addr))
		}
		if c.Http.Timeout != nil {
			opts = append(opts, http.Timeout(c.Http.Timeout.AsDuration()))
		}
	}
	srv := http.NewServer(opts...)

	var checks []health.Check
	if d != nil {
		checks = d.ReadinessChecks()
	} else {
		checks = (&data.Data{}).ReadinessChecks()
	}

	srv.Handle("/healthz", health.LivenessHandler())
	srv.Handle("/readyz", health.ReadinessHandlerWithLogger(logger, 3*time.Second, checks...))
	srv.Route("/").GET("/metrics", metricsHandler)

	// 注册Comment服务
	v1.RegisterCommentHTTPServer(srv, comment)

	return srv
}
