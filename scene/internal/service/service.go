package service

import (
	v1 "github.com/knoci/roaming-world/scene/api/scene/v1"
	"github.com/knoci/roaming-world/scene/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

// ProviderSet is service providers.
var ProviderSet = wire.NewSet(NewSceneService)

type SceneService struct {
	v1.UnimplementedSceneServer
	uc  *biz.SceneUsecase
	log *log.Helper
}
