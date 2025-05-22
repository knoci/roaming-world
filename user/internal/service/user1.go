package service

import (
	"context"

	v1 "user/api/user/v1"
	"user/internal/biz"

	"user/pkg/jwt"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

// UserService 用户服务实现
type UserService struct {
	v1.UnimplementedUserServer

	uc  *biz.UserUsecase
	log *log.Helper
}

// NewUserService 创建用户服务
func NewUserService(uc *biz.UserUsecase, logger log.Logger) *UserService {
	return &UserService{uc: uc, log: log.NewHelper(logger)}
}

// Register 用户注册
func (s *UserService) Register(ctx context.Context, req *v1.RegisterRequest) (*v1.RegisterReply, error) {
	user, err := s.uc.Register(ctx, req)
	if err != nil {
		return nil, err
	}

	// 生成JWT令牌
	token, err := jwt.GenerateToken(user.Uid, user.Name, user.Avatar, "")
	if err != nil {
		return nil, errors.InternalServer(v1.ErrorReason_INTERNAL_ERROR.String(), "生成令牌失败")
	}

	user.Token = token
	return user, nil
}

// SendVerificationCode 发送验证码
func (s *UserService) SendVerificationCode(ctx context.Context, req *v1.SendVerificationCodeRequest) (*v1.SendVerificationCodeReply, error) {
	code, err := s.uc.SendVerificationCode(ctx, req.Email)
	if err != nil {
		return nil, errors.InternalServer(v1.ErrorReason_INTERNAL_ERROR.String(), err.Error())
	}

	return &v1.SendVerificationCodeReply{Code: code}, nil
}

// Login 用户登录
func (s *UserService) Login(ctx context.Context, req *v1.LoginRequest) (*v1.LoginReply, error) {
	user, err := s.uc.Login(ctx, req)
	if err != nil {
		return nil, err
	}

	// 生成JWT令牌
	token, err := jwt.GenerateToken(user.Uid, user.Name, user.Avatar, "")
	if err != nil {
		return nil, errors.InternalServer(v1.ErrorReason_INTERNAL_ERROR.String(), "生成令牌失败")
	}

	user.Token = token
	return user, nil
}

// GetUserInfo 获取用户信息
func (s *UserService) GetUserInfo(ctx context.Context, req *v1.GetUserInfoRequest) (*v1.GetUserInfoReply, error) {
	// 从上下文中获取用户ID
	uid, ok := ctx.Value("uid").(string)
	if !ok || uid == "" {
		return nil, errors.Unauthorized(v1.ErrorReason_UNAUTHORIZED.String(), "未授权访问")
	}

	return s.uc.GetUserInfo(ctx, uid)
}

// FindUser 查找用户
func (s *UserService) FindUser(ctx context.Context, req *v1.FindUserRequest) (*v1.FindUserReply, error) {
	if req.Keyword == "" {
		return nil, errors.BadRequest(v1.ErrorReason_INVALID_ARGUMENT.String(), "关键字不能为空")
	}

	return s.uc.FindUser(ctx, req.Keyword)
}

// DeleteUser 删除用户
func (s *UserService) DeleteUser(ctx context.Context, req *v1.DeleteUserRequest) (*v1.DeleteUserReply, error) {
	// 从上下文中获取用户ID
	uid, ok := ctx.Value("uid").(string)
	if !ok || uid == "" {
		return nil, errors.Unauthorized(v1.ErrorReason_UNAUTHORIZED.String(), "未授权访问")
	}

	err := s.uc.DeleteUser(ctx, uid)
	if err != nil {
		return nil, err
	}

	return &v1.DeleteUserReply{}, nil
}

// UpdateUserInfo 更新用户信息
func (s *UserService) UpdateUserInfo(ctx context.Context, req *v1.UpdateUserInfoRequest) (*v1.UpdateUserInfoReply, error) {
	// 从上下文中获取用户ID
	uid, ok := ctx.Value("uid").(string)
	if !ok || uid == "" {
		return nil, errors.Unauthorized(v1.ErrorReason_UNAUTHORIZED.String(), "未授权访问")
	}

	return s.uc.UpdateUserInfo(ctx, uid, req)
}

// ResetPassword 重置密码
func (s *UserService) ResetPassword(ctx context.Context, req *v1.ResetPasswordRequest) (*v1.ResetPasswordReply, error) {
	err := s.uc.ResetPassword(ctx, req)
	if err != nil {
		return nil, err
	}

	return &v1.ResetPasswordReply{}, nil
}

// UploadAvatar 上传头像
func (s *UserService) UploadAvatar(ctx context.Context, req *v1.UploadAvatarRequest) (*v1.UploadAvatarReply, error) {
	// 从上下文中获取用户ID
	uid, ok := ctx.Value("uid").(string)
	if !ok || uid == "" {
		return nil, errors.Unauthorized(v1.ErrorReason_UNAUTHORIZED.String(), "未授权访问")
	}

	// 处理文件上传
	return s.uc.UploadAvatar(ctx, uid, req.File, req.Filename)
}

// MultiPost 多文件上传
func (s *UserService) MultiPost(ctx context.Context, req *v1.MultiPostRequest) (*v1.MultiPostReply, error) {
	// 处理多文件上传
	files := make([]biz.FileInfo, 0, len(req.Files))
	for _, file := range req.Files {
		files = append(files, biz.FileInfo{
			Content:  file.Content,
			Filename: file.Filename,
		})
	}

	return s.uc.MultiPost(ctx, files)
}

// 更新ProviderSet，添加UserService
func init() {
	ProviderSet = wire.NewSet(NewGreeterService, NewUserService)
}
