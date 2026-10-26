package service

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/transport/http"
	pb "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/biz"
	jwt "github.com/knoci/roaming-world/user/internal/pkg"
)

func NewUserService(user *biz.UserUsecase, logger log.Logger) *UserService {
	return &UserService{
		user: user,
		log:  log.NewHelper(logger),
	}
}

func (s *UserService) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterReply, error) {
	s.log.WithContext(ctx).Infof("Register received: Name=%s, Email=%s", req.Name, req.Email)
	user, err := s.user.Register(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("Register failed: %v", err)
		return nil, err
	}

	httpReq, _ := http.RequestFromServerContext(ctx)
	ua := httpReq.Header.Get("User-Agent")
	token, err := jwt.GenerateToken(ua, user.Uid, user.Name, req.Email, user.Avatar)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to generate token for user %s: %v", user.Name, err)
		return nil, biz.ErrInternalError
	}

	return &pb.RegisterReply{
		Uid:    user.Uid,
		Name:   user.Name,
		Avatar: user.Avatar,
		Token:  token,
	}, nil
}
func (s *UserService) SendVerificationCode(ctx context.Context, req *pb.SendVerificationCodeRequest) (*pb.SendVerificationCodeReply, error) {
	s.log.WithContext(ctx).Infof("SendVerificationCode received: Email=%s", req.Email)
	code, err := s.user.SendVerificationCode(ctx, req.Email)
	if err != nil {
		s.log.WithContext(ctx).Errorf("SendVerificationCode failed: %v", err)
		return nil, err
	}

	return &pb.SendVerificationCodeReply{
		Code: code,
	}, nil
}

func (s *UserService) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginReply, error) {
	s.log.WithContext(ctx).Infof("Login received: Email=%s", req.Email)
	reply, err := s.user.Login(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("Login failed: %v", err)
		return nil, err
	}

	httpReq, _ := http.RequestFromServerContext(ctx)
	ua := httpReq.Header.Get("User-Agent")
	token, err := jwt.GenerateToken(ua, reply.Uid, reply.Name, req.Email, reply.Avatar)
	if err != nil {
		s.log.WithContext(ctx).Errorf("failed to generate token for user %s: %v", reply.Name, err)
		return nil, biz.ErrInternalError
	}

	return &pb.LoginReply{
		Uid:    reply.Uid,
		Name:   reply.Name,
		Avatar: reply.Avatar,
		Token:  token,
	}, nil
}

func (s *UserService) FindUser(ctx context.Context, req *pb.FindUserRequest) (*pb.FindUserReply, error) {
	s.log.WithContext(ctx).Infof("userService: FindUser received: Uid=%s", req.Keyword)
	user, err := s.user.FindUser(ctx, req.Keyword)
	if err != nil {
		s.log.WithContext(ctx).Errorf("userService: FindUser failed: %v", err)
		return nil, err
	}
	return &pb.FindUserReply{
		Uid:    user.Uid,
		Name:   user.Name,
		Avatar: user.Avatar,
		Email:  user.Email,
	}, nil
}

func (s *UserService) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("userService: DeleteUser received: Uid=%s", claims.UID)
	err = s.user.DeleteUser(ctx, claims.UID)
	if err != nil {
		s.log.WithContext(ctx).Errorf("userService: DeleteUser failed: %v", err)
		return nil, err
	}
	return &pb.DeleteUserReply{Status: "删除成功"}, nil
}

func (s *UserService) UpdateUserInfo(ctx context.Context, req *pb.UpdateUserInfoRequest) (*pb.UpdateUserInfoReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("userService: UpdateUserInfo received: Uid=%s, Name=%s", claims.UID, claims.Name)
	user, err := s.user.UpdateUserInfo(ctx, claims.UID, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("userService: UpdateUserInfo failed: %v", err)
		return nil, err
	}
	return &pb.UpdateUserInfoReply{
		Uid:   user.Uid,
		Name:  user.Name,
		Email: user.Email,
	}, nil
}
func (s *UserService) ResetPassword(ctx context.Context, req *pb.ResetPasswordRequest) (*pb.ResetPasswordReply, error) {
	s.log.WithContext(ctx).Infof("userService: ResetPassword received: Email=%s", req.Email)
	err := s.user.ResetPassword(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("userService: ResetPassword failed: %v", err)
		return nil, err
	}
	return &pb.ResetPasswordReply{}, nil
}
func (s *UserService) UploadAvatar(ctx context.Context, req *pb.UploadAvatarRequest) (*pb.UploadAvatarReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("userService: UploadAvatar received: Uid=%s", claims.UID)
	httpReq, ok := http.RequestFromServerContext(ctx)
	if !ok || httpReq == nil {
		return nil, biz.ErrUnauthorized
	}
	_, file, err := httpReq.FormFile("avatar")
	if err != nil || file == nil {
		return nil, biz.ErrInvalidArgument
	}
	contentType := file.Header.Get("Content-Type")
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/gif" {
		return nil, biz.ErrInvalidArgument
	}
	if file.Size > 3*1024*1024 {
		return nil, biz.ErrInvalidArgument
	}
	rep, err := s.user.UploadAvatar(ctx, claims.UID, file)
	if err != nil {
		s.log.WithContext(ctx).Errorf("userService: UploadAvatar failed: %v", err)
		return nil, err
	}
	return rep, nil
}
func (s *UserService) ConfirmUser(ctx context.Context, req *pb.ConfirmUserRequest) (*pb.ConfirmUserReply, error) {
	if err := s.user.CheckUserExist(ctx, req.Name, req.Email); err != nil {
		return nil, err
	}
	return &pb.ConfirmUserReply{Status: "验证成功"}, nil
}
func (s *UserService) ConfirmEmail(ctx context.Context, req *pb.ConfirmEmailRequest) (*pb.ConfirmEmailReply, error) {
	s.log.WithContext(ctx).Infof("userService: VerifyCode received: Email=%s, Code=%s", req.Email, req.Code)
	rep, err := s.user.ConfirmEmail(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("userService: VerifyCode failed: %v", err)
		return rep, err
	}
	return rep, nil
}

func GetJwtClaim(ctx context.Context) (*jwt.Claims, error) {
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

	claims, err := jwt.ParseToken(ua, parts[1])
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	return claims, nil
}
