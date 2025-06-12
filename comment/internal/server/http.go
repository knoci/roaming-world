package server

import (
	"github.com/knoci/roaming-world/comment/internal/conf"
	"github.com/knoci/roaming-world/comment/internal/service"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/transport/http"
	v1 "github.com/knoci/roaming-world/comment/api/comment/v1"
)

// NewHTTPServer new an HTTP server.
func NewHTTPServer(c *conf.Server, comment *service.CommentService, logger log.Logger) *http.Server {
	var opts = []http.ServerOption{
		http.Middleware(
			recovery.Recovery(),
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

	// 注册Comment服务
	v1.RegisterCommentHTTPServer(srv, comment)
	
	return srv
}
