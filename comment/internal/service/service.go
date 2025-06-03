package service

import (
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/comment/internal/biz"
)

// ProviderSet is service providers.
var ProviderSet = wire.NewSet(NewGreeterService, NewCommentService)

// GreeterService is a greeter service.
type GreeterService struct {
	UnimplementedGreeterServer

	uc  *biz.GreeterUsecase
	log *log.Helper
}

// NewGreeterService new a greeter service.
func NewGreeterService(uc *biz.GreeterUsecase, logger log.Logger) *GreeterService {
	return &GreeterService{uc: uc, log: log.NewHelper(logger)}
}
