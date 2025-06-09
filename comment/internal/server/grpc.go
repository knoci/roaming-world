package server

import (
	"github.com/knoci/roaming-world/comment/internal/conf"
	"github.com/knoci/roaming-world/comment/internal/service"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	v1 "github.com/knoci/roaming-world/comment/api/comment/v1"
)

// NewGRPCServer new a gRPC server.
func NewGRPCServer(c *conf.Server, comment *service.CommentService, logger log.Logger) *grpc.Server {
	var opts = []grpc.ServerOption{
		grpc.Middleware(
			recovery.Recovery(),
		),
	}
	if c.Grpc.Network != "" {
		opts = append(opts, grpc.Network(c.Grpc.Network))
	}
	if c.Grpc.Addr != "" {
		opts = append(opts, grpc.Address(c.Grpc.Addr))
	}
	if c.Grpc.Timeout != nil {
		opts = append(opts, grpc.Timeout(c.Grpc.Timeout.AsDuration()))
	}
	srv := grpc.NewServer(opts...)

	// 注册Comment服务
	v1.RegisterCommentServiceServer(srv, comment)

	return srv
}
