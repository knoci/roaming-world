package service

import (
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	pb "github.com/knoci/roaming-world/audiobook/api/audiobook/v1"
	"github.com/knoci/roaming-world/audiobook/internal/biz"
)

// ProviderSet is service providers.
var ProviderSet = wire.NewSet(NewAudiobookService)

// AudiobookService 有声书服务实现
type AudiobookService struct {
	pb.UnimplementedAudiobookServer
	audiobook *biz.AudiobookUsecase
	log       *log.Helper
}
