package service

import (
	"context"

	pb "github.com/knoci/roaming-world/comment/api/comment/v1"
)

func NewCommentService() *CommentService {
	return &CommentService{}
}

func (s *CommentService) CreateComment(ctx context.Context, req *pb.CreateCommentRequest) (*pb.CreateCommentReply, error) {
	return &pb.CreateCommentReply{}, nil
}
func (s *CommentService) DeleteComment(ctx context.Context, req *pb.DeleteCommentRequest) (*pb.DeleteCommentReply, error) {
	return &pb.DeleteCommentReply{}, nil
}
func (s *CommentService) LikeComment(ctx context.Context, req *pb.LikeCommentRequest) (*pb.LikeCommentReply, error) {
	return &pb.LikeCommentReply{}, nil
}
func (s *CommentService) GetCommentList(ctx context.Context, req *pb.GetCommentListRequest) (*pb.GetCommentListReply, error) {
	return &pb.GetCommentListReply{}, nil
}
func (s *CommentService) GetCommentListWithReplies(ctx context.Context, req *pb.GetCommentListWithRepliesRequest) (*pb.GetCommentListWithRepliesReply, error) {
	return &pb.GetCommentListWithRepliesReply{}, nil
}
func (s *CommentService) GetReplyList(ctx context.Context, req *pb.GetReplyListRequest) (*pb.GetReplyListReply, error) {
	return &pb.GetReplyListReply{}, nil
}
