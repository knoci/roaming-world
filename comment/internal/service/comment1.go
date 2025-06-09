package service

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"
	pb "github.com/knoci/roaming-world/comment/api/comment/v1"
	"github.com/knoci/roaming-world/comment/internal/biz"
	"github.com/knoci/roaming-world/comment/internal/pkg"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type CommentService struct {
	pb.UnimplementedCommentServiceServer

	uc  *biz.CommentUsecase
	log *log.Helper
}

// 将业务错误转换为错误原因枚举
func convertError(err error) pb.ErrorReason {
	if err == nil {
		return pb.ErrorReason_UNKNOWN
	}

	e := errors.FromError(err)
	switch e.Reason {
	case "USER_NOT_FOUND":
		return pb.ErrorReason_USER_NOT_FOUND
	case "COMMENT_NOT_FOUND":
		return pb.ErrorReason_COMMENT_NOT_FOUND
	case "NO_PERMISSION":
		return pb.ErrorReason_NO_PERMISSION
	case "INTERNAL_ERROR":
		return pb.ErrorReason_INTERNAL_ERROR
	case "UNAUTHORIZED":
		return pb.ErrorReason_UNAUTHORIZED
	case "PARENT_COMMENT_NOT_FOUND":
		return pb.ErrorReason_PARENT_COMMENT_NOT_FOUND
	default:
		return pb.ErrorReason_UNKNOWN
	}
}

// NewCommentService 创建评论服务
func NewCommentService(uc *biz.CommentUsecase, logger log.Logger) *CommentService {
	return &CommentService{
		uc:  uc,
		log: log.NewHelper(logger),
	}
}

// CreateComment 创建评论
func (s *CommentService) CreateComment(ctx context.Context, req *pb.CreateCommentRequest) (*pb.CreateCommentReply, error) {
	// 验证JWT令牌
	claims, err := pkg.ParseToken(req.Token)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to parse token: %v", err)
		return &pb.CreateCommentReply{
			ErrorReason: pb.ErrorReason_UNAUTHORIZED,
		}, biz.ErrUnauthorized
	}

	// 创建评论
	comment, err := s.uc.CreateComment(ctx, claims.UID, req.Target, req.Content, req.Replycid, req.Replyname)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to create comment: %v", err)
		return &pb.CreateCommentReply{
			ErrorReason: convertError(err),
		}, err
	}

	// 转换为API响应
	createdAt := timestamppb.New(comment.CreatedAt)
	updatedAt := timestamppb.New(comment.UpdatedAt)

	return &pb.CreateCommentReply{
		Comment: &pb.CommentMessage{
			Cid:       comment.CID,
			Target:    comment.Target,
			Content:   comment.Content,
			Likes:     int32(comment.Likes),
			Uid:       comment.UID,
			Name:      comment.Name,
			Avatar:    comment.Avatar,
			Replycid:  comment.Replycid,
			Replyname: comment.Replyname,
			Time:      comment.Time,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
	}, nil
}

// DeleteComment 删除评论
func (s *CommentService) DeleteComment(ctx context.Context, req *pb.DeleteCommentRequest) (*pb.DeleteCommentReply, error) {
	// 验证JWT令牌
	claims, err := pkg.ParseToken(req.Token)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to parse token: %v", err)
		return &pb.DeleteCommentReply{
			Status:      "failed",
			ErrorReason: pb.ErrorReason_UNAUTHORIZED,
		}, biz.ErrUnauthorized
	}

	// 删除评论
	err = s.uc.DeleteComment(ctx, claims.UID, req.Cid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to delete comment: %v", err)
		return &pb.DeleteCommentReply{
			Status:      "failed",
			ErrorReason: convertError(err),
		}, err
	}

	return &pb.DeleteCommentReply{
		Status: "success",
	}, nil
}

// LikeComment 点赞评论
func (s *CommentService) LikeComment(ctx context.Context, req *pb.LikeCommentRequest) (*pb.LikeCommentReply, error) {
	// 验证JWT令牌
	claims, err := pkg.ParseToken(req.Token)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to parse token: %v", err)
		return &pb.LikeCommentReply{
			Status:      "failed",
			ErrorReason: pb.ErrorReason_UNAUTHORIZED,
		}, biz.ErrUnauthorized
	}

	// 点赞评论
	err = s.uc.LikeComment(ctx, claims.UID, req.Cid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to like comment: %v", err)
		return &pb.LikeCommentReply{
			Status:      "failed",
			ErrorReason: convertError(err),
		}, err
	}

	return &pb.LikeCommentReply{
		Status: "success",
	}, nil
}

// GetCommentList 获取评论列表
func (s *CommentService) GetCommentList(ctx context.Context, req *pb.GetCommentListRequest) (*pb.GetCommentListReply, error) {
	// 获取评论列表
	comments, err := s.uc.GetCommentList(ctx, req.Aid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to get comment list: %v", err)
		return &pb.GetCommentListReply{
			ErrorReason: convertError(err),
		}, err
	}

	// 转换为API响应
	result := make([]*pb.CommentMessage, 0, len(comments))
	for _, comment := range comments {
		createdAt := timestamppb.New(comment.CreatedAt)
		updatedAt := timestamppb.New(comment.UpdatedAt)

		result = append(result, &pb.CommentMessage{
			Cid:       comment.CID,
			Target:    comment.Target,
			Content:   comment.Content,
			Likes:     int32(comment.Likes),
			Uid:       comment.UID,
			Name:      comment.Name,
			Avatar:    comment.Avatar,
			Replycid:  comment.Replycid,
			Replyname: comment.Replyname,
			Time:      comment.Time,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}

	return &pb.GetCommentListReply{
		Comments: result,
	}, nil
}

// GetCommentListWithReplies 获取评论列表及其回复
func (s *CommentService) GetCommentListWithReplies(ctx context.Context, req *pb.GetCommentListWithRepliesRequest) (*pb.GetCommentListWithRepliesReply, error) {
	// 获取评论列表及其回复
	comments, err := s.uc.GetCommentListWithReplies(ctx, req.Aid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to get comment list with replies: %v", err)
		return &pb.GetCommentListWithRepliesReply{
			ErrorReason: convertError(err),
		}, err
	}

	// 转换为API响应
	result := make([]*pb.CommentMessage, 0, len(comments))
	for _, comment := range comments {
		createdAt := timestamppb.New(comment.CreatedAt)
		updatedAt := timestamppb.New(comment.UpdatedAt)

		result = append(result, &pb.CommentMessage{
			Cid:       comment.CID,
			Target:    comment.Target,
			Content:   comment.Content,
			Likes:     int32(comment.Likes),
			Uid:       comment.UID,
			Name:      comment.Name,
			Avatar:    comment.Avatar,
			Replycid:  comment.Replycid,
			Replyname: comment.Replyname,
			Time:      comment.Time,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}

	return &pb.GetCommentListWithRepliesReply{
		Comments: result,
	}, nil
}

// GetReplyList 获取回复列表
func (s *CommentService) GetReplyList(ctx context.Context, req *pb.GetReplyListRequest) (*pb.GetReplyListReply, error) {
	// 获取回复列表
	replies, err := s.uc.GetReplyList(ctx, req.Cid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to get reply list: %v", err)
		return &pb.GetReplyListReply{
			ErrorReason: convertError(err),
		}, err
	}

	// 转换为API响应
	result := make([]*pb.CommentMessage, 0, len(replies))
	for _, reply := range replies {
		createdAt := timestamppb.New(reply.CreatedAt)
		updatedAt := timestamppb.New(reply.UpdatedAt)

		result = append(result, &pb.CommentMessage{
			Cid:       reply.CID,
			Target:    reply.Target,
			Content:   reply.Content,
			Likes:     int32(reply.Likes),
			Uid:       reply.UID,
			Name:      reply.Name,
			Avatar:    reply.Avatar,
			Replycid:  reply.Replycid,
			Replyname: reply.Replyname,
			Time:      reply.Time,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}

	return &pb.GetReplyListReply{
		Comments: result,
	}, nil
}

func GetJwtClaim(ctx context.Context) (*jwt.Claims, error) {
	httpReq, _ := http.RequestFromServerContext(ctx)
	authHeader := httpReq.Header.Get("Authorization")

	if len(authHeader) == 0 {
		return nil, biz.ErrUnauthorized
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if !(len(parts) == 2 && parts[0] == "Bearer") {
		return nil, biz.ErrUnauthorized
	}

	ua := httpReq.Header.Get("User-Agent")
	claims, err := jwt.ParseToken(ua, parts[1])
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	return claims, nil
}
