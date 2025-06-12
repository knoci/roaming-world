package service

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"
	pb "github.com/knoci/roaming-world/comment/api/comment/v1"
	"github.com/knoci/roaming-world/comment/internal/biz"
	"github.com/knoci/roaming-world/comment/internal/pkg"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func NewCommentService(comment *biz.CommentUsecase, logger log.Logger) *CommentService {
	return &CommentService{
		uc : comment,
		log:  log.NewHelper(logger),
	}
}

func (s *CommentService) CreateComment(ctx context.Context, req *pb.CreateCommentRequest) (*pb.CreateCommentReply, error) {
	// 验证JWT令牌
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}

	// 创建评论
	comment, err := s.uc.CreateComment(ctx, claims.UID, req.Target, req.Content, req.Replycid, req.Replyname)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to create comment: %v", err)
		return nil, err
	}

	
	createdAt := timestamppb.New(comment.CreatedAt).String()
	updatedAt := timestamppb.New(comment.UpdatedAt).String()

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
func (s *CommentService) DeleteComment(ctx context.Context, req *pb.DeleteCommentRequest) (*pb.DeleteCommentReply, error) {
	// 验证JWT令牌
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}

	// 删除评论
	err = s.uc.DeleteComment(ctx, claims.UID, req.Cid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to delete comment: %v", err)
		return nil, err
	}

	return &pb.DeleteCommentReply{
		Status: "删除成功",
	}, nil
}
func (s *CommentService) LikeComment(ctx context.Context, req *pb.LikeCommentRequest) (*pb.LikeCommentReply, error) {
	// 验证JWT令牌
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}

	// 点赞评论
	err = s.uc.LikeComment(ctx, claims.UID, req.Cid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to like comment: %v", err)
		return nil, err
	}

	return &pb.LikeCommentReply{
		Status: "点赞成功",
	}, nil
}
func (s *CommentService) GetCommentList(ctx context.Context, req *pb.GetCommentListRequest) (*pb.GetCommentListReply, error) {
	// 获取评论列表
	comments, err := s.uc.GetCommentList(ctx, req.Aid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to get comment list: %v", err)
		return nil, err
	}

	// 转换为API响应
	result := make([]*pb.CommentMessage, 0, len(comments))
	for _, comment := range comments {
		createdAt := timestamppb.New(comment.CreatedAt).String()
		updatedAt := timestamppb.New(comment.UpdatedAt).String()

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
func (s *CommentService) GetCommentListWithReplies(ctx context.Context, req *pb.GetCommentListWithRepliesRequest) (*pb.GetCommentListWithRepliesReply, error) {
	// 获取评论列表及其回复
	comments, err := s.uc.GetCommentListWithReplies(ctx, req.Aid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to get comment list with replies: %v", err)
		return nil, err
	}

	result := make([]*pb.CommentMessage, 0, len(comments))
	for _, comment := range comments {
		createdAt := timestamppb.New(comment.CreatedAt).String()
		updatedAt := timestamppb.New(comment.UpdatedAt).String()

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
func (s *CommentService) GetReplyList(ctx context.Context, req *pb.GetReplyListRequest) (*pb.GetReplyListReply, error) {
	// 获取回复列表
	replies, err := s.uc.GetReplyList(ctx, req.Cid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to get reply list: %v", err)
		return nil, err
	}

	// 转换为API响应
	result := make([]*pb.CommentMessage, 0, len(replies))
	for _, reply := range replies {
		createdAt := timestamppb.New(reply.CreatedAt).String()
		updatedAt := timestamppb.New(reply.UpdatedAt).String()

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

func GetJwtClaim(ctx context.Context) (*pkg.Claims, error) {
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
	claims, err := pkg.ParseToken(ua, parts[1])
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	return claims, nil
}