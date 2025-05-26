package server

import (
	"context"

	v1 "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/conf"
	pkg "github.com/knoci/roaming-world/user/internal/pkg/jwt"
	nacos "github.com/knoci/roaming-world/user/internal/pkg/nacos"
	"github.com/knoci/roaming-world/user/internal/service"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/auth/jwt"
	mmd "github.com/go-kratos/kratos/v2/middleware/metadata"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/selector"
	"github.com/go-kratos/kratos/v2/transport/http"
	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// NewHTTPServer new an HTTP server.
func NewHTTPServer(c *conf.Server, user *service.UserService, logger log.Logger) *http.Server {
	// JWT 中间件配置
	cfg := nacos.GetConfig()
	jwtMiddleware := jwt.Server(
		func(token *jwtv5.Token) (interface{}, error) {
			return nacos.GetConfigString(cfg, "jwt.secret"), nil
		},
		jwt.WithSigningMethod(jwtv5.SigningMethodHS256),
		jwt.WithClaims(func() jwtv5.Claims {
			return &pkg.Claims{}
		}),
	)
	var opts = []http.ServerOption{
		http.Middleware(
			recovery.Recovery(),
			mmd.Server(),
			selector.Server(
				jwtMiddleware,
			).Match(func(ctx context.Context, operation string) bool {
				noAuthOperations := map[string]bool{
					"/user.v1.User/Register":             true,
					"/user.v1.User/Login":                true,
					"/user.v1.User/SendVerificationCode": true,
				}
				return !noAuthOperations[operation]
			}).Build(),
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
	v1.RegisterUserHTTPServer(srv, user)
	return srv
}
