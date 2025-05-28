package service

import (
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	pb "github.com/knoci/roaming-world/food/api/food/v1"
	"github.com/knoci/roaming-world/food/internal/biz"
)

// ProviderSet is service providers.
var ProviderSet = wire.NewSet(NewFoodService)

type FoodService struct {
	pb.UnimplementedFoodServer
	food *biz.FoodUsecase
	log  *log.Helper
}
