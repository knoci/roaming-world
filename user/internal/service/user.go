package service

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/go-kratos/kratos/v2/transport/http"
	pb "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/biz"
	jwt "github.com/knoci/roaming-world/user/internal/pkg"
)

func NewUserService(user *biz.UserUsecase, social *biz.SocialUsecase, logger log.Logger) *UserService {
	return &UserService{
		user:   user,
		social: social,
		log:    log.NewHelper(logger),
	}
}

func (s *UserService) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterReply, error) {
	s.log.WithContext(ctx).Infof("Register received: Name=%s, Email=%s", req.Name, req.Email)
	user, err := s.user.Register(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("Register failed: %v", err)
		return nil, err
	}

	token, err := jwt.GenerateToken(requestUserAgent(ctx), user.Uid, user.Name, req.Email, user.Avatar, "user")
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
	if tr, ok := transport.FromServerContext(ctx); ok && tr.Kind() == transport.KindHTTP {
		tr.ReplyHeader().Set("Cache-Control", "no-store")
	}
	target, err := verificationTarget(ctx, req.Email, req.Purpose)
	if err != nil {
		return nil, err
	}
	if err := s.user.SendVerificationCode(ctx, target); err != nil {
		return nil, err
	}
	return &pb.SendVerificationCodeReply{}, nil
}

func (s *UserService) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginReply, error) {
	s.log.WithContext(ctx).Infof("Login received: Email=%s", req.Email)
	reply, err := s.user.Login(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("Login failed: %v", err)
		return nil, err
	}

	role, roleErr := s.user.GetRole(ctx, reply.Uid)
	if roleErr != nil {
		s.log.WithContext(ctx).Errorf("failed to get role for user %s: %v", reply.Name, roleErr)
		return nil, biz.ErrInternalError
	}
	token, err := jwt.GenerateToken(requestUserAgent(ctx), reply.Uid, reply.Name, req.Email, reply.Avatar, role)
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
	target, err := verificationTarget(ctx, req.Email, req.Purpose)
	if err != nil {
		return nil, err
	}
	if err := s.user.ConfirmEmail(ctx, target, req.Code); err != nil {
		return nil, err
	}
	return &pb.ConfirmEmailReply{Status: "验证成功"}, nil
}

func verificationTarget(ctx context.Context, email, purpose string) (biz.VerificationTarget, error) {
	if purpose == "" {
		purpose = biz.VerificationRegister
	}
	target := biz.VerificationTarget{Email: email, Purpose: purpose}
	if purpose == biz.VerificationChangeEmail {
		claims, err := GetJwtClaim(ctx)
		if err != nil {
			return biz.VerificationTarget{}, err
		}
		target.UID = claims.UID
	}
	return target, nil
}

func requestUserAgent(ctx context.Context) string {
	if tr, ok := transport.FromServerContext(ctx); ok {
		return tr.RequestHeader().Get("User-Agent")
	}
	return ""
}

func GetJwtClaim(ctx context.Context) (*jwt.Claims, error) {
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return nil, biz.ErrUnauthorized
	}
	authHeader := tr.RequestHeader().Get("Authorization")
	parts := strings.SplitN(authHeader, " ", 2)
	if !(len(parts) == 2 && parts[0] == "Bearer") {
		return nil, biz.ErrUnauthorized
	}

	claims, err := jwt.ParseToken(tr.RequestHeader().Get("User-Agent"), parts[1])
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	return claims, nil
}
