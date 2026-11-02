package service

import (
	"context"

	pb "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/biz"
)

func (s *UserService) FollowUser(ctx context.Context, req *pb.FollowUserRequest) (*pb.FollowUserReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	if _, err := s.social.Follow(ctx, claims.UID, req.Uid); err != nil {
		return nil, err
	}
	return &pb.FollowUserReply{Following: true}, nil
}

func (s *UserService) UnfollowUser(ctx context.Context, req *pb.UnfollowUserRequest) (*pb.UnfollowUserReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	if err := s.social.Unfollow(ctx, claims.UID, req.Uid); err != nil {
		return nil, err
	}
	return &pb.UnfollowUserReply{Following: false}, nil
}

func (s *UserService) GetFollowers(ctx context.Context, req *pb.GetFollowersRequest) (*pb.GetFollowersReply, error) {
	users, total, err := s.social.Followers(ctx, req.Uid, int(req.Page), int(req.Limit))
	if err != nil {
		return nil, err
	}
	return &pb.GetFollowersReply{Users: toSummaries(users), Total: total}, nil
}

func (s *UserService) GetFollowing(ctx context.Context, req *pb.GetFollowingRequest) (*pb.GetFollowingReply, error) {
	users, total, err := s.social.Following(ctx, req.Uid, int(req.Page), int(req.Limit))
	if err != nil {
		return nil, err
	}
	return &pb.GetFollowingReply{Users: toSummaries(users), Total: total}, nil
}

func (s *UserService) GetFollowingUIDs(ctx context.Context, req *pb.GetFollowingUIDsRequest) (*pb.GetFollowingUIDsReply, error) {
	uids, err := s.social.FollowingUIDs(ctx, req.Uid)
	if err != nil {
		return nil, err
	}
	return &pb.GetFollowingUIDsReply{Uids: uids}, nil
}

func (s *UserService) CreateNotification(ctx context.Context, req *pb.CreateNotificationRequest) (*pb.CreateNotificationReply, error) {
	if err := s.social.CreateNotification(ctx, &biz.Notification{
		UID: req.Uid, Type: req.Type, ActorUID: req.ActorUid, ActorName: req.ActorName, Payload: req.Payload,
	}); err != nil {
		return nil, err
	}
	return &pb.CreateNotificationReply{Status: "created"}, nil
}

func (s *UserService) GetNotifications(ctx context.Context, req *pb.GetNotificationsRequest) (*pb.GetNotificationsReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	items, total, unread, err := s.social.Notifications(ctx, claims.UID, int(req.Page), int(req.Limit))
	if err != nil {
		return nil, err
	}
	result := make([]*pb.NotificationMessage, 0, len(items))
	for _, n := range items {
		result = append(result, &pb.NotificationMessage{
			Id: n.ID, Type: n.Type, ActorUid: n.ActorUID, ActorName: n.ActorName,
			Payload: n.Payload, Read: n.Read, CreatedAt: n.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return &pb.GetNotificationsReply{Notifications: result, Total: total, Unread: unread}, nil
}

func (s *UserService) MarkNotificationRead(ctx context.Context, req *pb.MarkNotificationReadRequest) (*pb.MarkNotificationReadReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	if err := s.social.MarkNotificationRead(ctx, claims.UID, req.Id); err != nil {
		return nil, err
	}
	return &pb.MarkNotificationReadReply{Status: "read"}, nil
}

func toSummaries(users []*biz.UserSummary) []*pb.UserSummary {
	result := make([]*pb.UserSummary, 0, len(users))
	for _, u := range users {
		result = append(result, &pb.UserSummary{Uid: u.UID, Name: u.Name, Avatar: u.Avatar})
	}
	return result
}
