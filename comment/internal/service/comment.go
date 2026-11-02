package service

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/transport/http"
	pb "github.com/knoci/roaming-world/comment/api/comment/v1"
	"github.com/knoci/roaming-world/comment/internal/biz"
	"github.com/knoci/roaming-world/comment/internal/pkg"
)

func NewCommentService(comment *biz.CommentUsecase, logger log.Logger) *CommentService {
	return &CommentService{
		uc:  comment,
		log: log.NewHelper(logger),
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

	createdAt := comment.CreatedAt.Format("2006-01-02 15:04:05")
	updatedAt := comment.UpdatedAt.Format("2006-01-02 15:04:05")

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
func (s *CommentService) UnlikeComment(ctx context.Context, req *pb.UnlikeCommentRequest) (*pb.UnlikeCommentReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	if err := s.uc.UnlikeComment(ctx, claims.UID, req.Cid); err != nil {
		return nil, err
	}
	return &pb.UnlikeCommentReply{Status: "取消点赞成功"}, nil
}

func (s *CommentService) GetCommentList(ctx context.Context, req *pb.GetCommentListRequest) (*pb.GetCommentListReply, error) {
	comments, total, err := s.uc.GetCommentList(ctx, req.Aid, int(req.Page), int(req.Limit))
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to get comment list: %v", err)
		return nil, err
	}
	result := make([]*pb.CommentMessage, 0, len(comments))
	for _, comment := range comments {
		result = append(result, toMessage(comment))
	}
	return &pb.GetCommentListReply{Comments: result, Total: total}, nil
}

func (s *CommentService) GetCommentListWithReplies(ctx context.Context, req *pb.GetCommentListWithRepliesRequest) (*pb.GetCommentListWithRepliesReply, error) {
	comments, total, err := s.uc.GetCommentListWithReplies(ctx, req.Aid, int(req.Page), int(req.Limit))
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to get comment list with replies: %v", err)
		return nil, err
	}
	result := make([]*pb.CommentMessage, 0, len(comments))
	for _, comment := range comments {
		result = append(result, toMessage(comment))
	}
	return &pb.GetCommentListWithRepliesReply{Comments: result, Total: total}, nil
}

func (s *CommentService) GetReplyList(ctx context.Context, req *pb.GetReplyListRequest) (*pb.GetReplyListReply, error) {
	replies, total, err := s.uc.GetReplyList(ctx, req.Cid, int(req.Page), int(req.Limit))
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to get reply list: %v", err)
		return nil, err
	}
	result := make([]*pb.CommentMessage, 0, len(replies))
	for _, reply := range replies {
		result = append(result, toMessage(reply))
	}
	return &pb.GetReplyListReply{Comments: result, Total: total}, nil
}

func toMessage(comment *biz.Comment) *pb.CommentMessage {
	if comment == nil {
		return nil
	}
	return &pb.CommentMessage{
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
		CreatedAt: comment.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt: comment.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
}

func (s *CommentService) ReportComment(ctx context.Context, req *pb.ReportCommentRequest) (*pb.ReportCommentReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	report, err := s.uc.ReportComment(ctx, claims.UID, req.Cid, req.Reason)
	if err != nil {
		return nil, err
	}
	return &pb.ReportCommentReply{Status: "reported", ReportId: report.ID}, nil
}

func (s *CommentService) ListCommentReports(ctx context.Context, req *pb.ListCommentReportsRequest) (*pb.ListCommentReportsReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	if claims.Role != "admin" {
		return nil, biz.ErrNoPermission
	}
	reports, total, err := s.uc.ListReports(ctx, req.Status, int(req.Page), int(req.Limit))
	if err != nil {
		return nil, err
	}
	result := make([]*pb.CommentReportMessage, 0, len(reports))
	for _, report := range reports {
		result = append(result, &pb.CommentReportMessage{
			Id: report.ID, Cid: report.CID, ReporterUid: report.ReporterUID,
			Reason: report.Reason, Status: report.Status, ReviewNote: report.ReviewNote,
			CreatedAt: report.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return &pb.ListCommentReportsReply{Reports: result, Total: total}, nil
}

func (s *CommentService) ResolveCommentReport(ctx context.Context, req *pb.ResolveCommentReportRequest) (*pb.ResolveCommentReportReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	if claims.Role != "admin" {
		return nil, biz.ErrNoPermission
	}
	report, err := s.uc.ResolveReport(ctx, claims.UID, req.Id, req.Action, req.Note)
	if err != nil {
		return nil, err
	}
	return &pb.ResolveCommentReportReply{Status: report.Status}, nil
}

func GetJwtClaim(ctx context.Context) (*pkg.Claims, error) {
	var authHeader, ua string
	if req, ok := http.RequestFromServerContext(ctx); ok && req != nil {
		authHeader = req.Header.Get("Authorization")
		ua = req.Header.Get("User-Agent")
	} else if md, ok := metadata.FromServerContext(ctx); ok {
		authHeader = md.Get("authorization")
		ua = md.Get("user-agent")
	}

	if len(authHeader) == 0 {
		return nil, biz.ErrUnauthorized
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if !(len(parts) == 2 && parts[0] == "Bearer") {
		return nil, biz.ErrUnauthorized
	}

	claims, err := pkg.ParseToken(ua, parts[1])
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	return claims, nil
}
