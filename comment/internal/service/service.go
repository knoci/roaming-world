package service

import (
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	pb "github.com/knoci/roaming-world/comment/api/comment/v1"
	"github.com/knoci/roaming-world/comment/internal/biz"
)

// ProviderSet is service providers.
var ProviderSet = wire.NewSet(NewCommentService)

type CommentService struct {
	pb.UnimplementedCommentServiceServer

	uc  *biz.CommentUsecase
	log *log.Helper
}
