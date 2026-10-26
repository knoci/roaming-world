package service

import (
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	pb "github.com/knoci/roaming-world/article/api/article/v1"
	"github.com/knoci/roaming-world/article/internal/biz"
)

// ProviderSet is service providers.
var ProviderSet = wire.NewSet(NewArticleService)

type ArticleService struct {
	pb.UnimplementedArticleServer
	uc  *biz.ArticleUsecase
	log *log.Helper
}
