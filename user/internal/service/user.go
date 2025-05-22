package service

import (
	"context"

	pb "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
)

func NewUserService(user *biz.UserUsecase, logger log.Logger) *UserService {
	return &UserService{
		user: user,
		log:  log.NewHelper(logger),
	}
}

func (s *UserService) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterReply, error) {
	return &pb.RegisterReply{}, nil
}
func (s *UserService) SendVerificationCode(ctx context.Context, req *pb.SendVerificationCodeRequest) (*pb.SendVerificationCodeReply, error) {
	return &pb.SendVerificationCodeReply{}, nil
}
func (s *UserService) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginReply, error) {
	return &pb.LoginReply{}, nil
}
func (s *UserService) FindUser(ctx context.Context, req *pb.FindUserRequest) (*pb.FindUserReply, error) {
	return &pb.FindUserReply{}, nil
}
func (s *UserService) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserReply, error) {
	return &pb.DeleteUserReply{}, nil
}
func (s *UserService) UpdateUserInfo(ctx context.Context, req *pb.UpdateUserInfoRequest) (*pb.UpdateUserInfoReply, error) {
	return &pb.UpdateUserInfoReply{}, nil
}
func (s *UserService) ResetPassword(ctx context.Context, req *pb.ResetPasswordRequest) (*pb.ResetPasswordReply, error) {
	return &pb.ResetPasswordReply{}, nil
}
func (s *UserService) UploadAvatar(ctx context.Context, req *pb.UploadAvatarRequest) (*pb.UploadAvatarReply, error) {
	return &pb.UploadAvatarReply{}, nil
}
func (s *UserService) ConfirmUser(ctx context.Context, req *pb.ConfirmUserRequest) (*pb.ConfirmUserReply, error) {
	return &pb.ConfirmUserReply{}, nil
}
func (s *UserService) ConfirmEmail(ctx context.Context, req *pb.ConfirmEmailRequest) (*pb.ConfirmEmailReply, error) {
	return &pb.ConfirmEmailReply{}, nil
}
