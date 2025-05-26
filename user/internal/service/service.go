package service

import (
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	pb "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/biz"
)

// ProviderSet is service providers.
var ProviderSet = wire.NewSet(NewUserService)

type UserService struct {
	pb.UnimplementedUserServer
	log  *log.Helper
	user *biz.UserUsecase
}

