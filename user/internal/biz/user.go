package biz

import (
	"context"
	"time"

	v1 "github.com/knoci/roaming-world/user/api/user/v1"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
)

var (
	// 错误定义
	ErrUserNotFound          = errors.NotFound(v1.ErrorReason_NOT_FOUND.String(), "不存在")
	ErrInvalidArgument       = errors.BadRequest(v1.ErrorReason_INVALID_ARGUMENT.String(), "请求参数错误")
	ErrEmailAlreadyExists    = errors.Conflict(v1.ErrorReason_EMAIL_ALREADY_EXISTS.String(), "邮箱已被注册")
	ErrUsernameAlreadyExists = errors.Conflict(v1.ErrorReason_USERNAME_ALREADY_EXISTS.String(), "用户名已存在")
	ErrVerificationExpired   = errors.BadRequest(v1.ErrorReason_VERIFICATION_CODE_EXPIRED.String(), "验证码已过期或不存在")
	ErrIncorrectPassword     = errors.Unauthorized(v1.ErrorReason_INCORRECT_PASSWORD.String(), "密码错误")
	ErrUnauthorized          = errors.Unauthorized(v1.ErrorReason_UNAUTHORIZED.String(), "未授权访问")
	ErrInternalError         = errors.InternalServer(v1.ErrorReason_INTERNAL_ERROR.String(), "服务器内部错误")
)

// User 用户实体
type User struct {
	UID       string
	Name      string
	Email     string
	Password  string
	Avatar    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UserRepo 用户仓库接口
type UserRepo interface {
	Create(ctx context.Context, user *User) (*User, error)
	Update(ctx context.Context, user *User) (*User, error)
	FindByUID(ctx context.Context, uid string) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByKeyword(ctx context.Context, keyword string) (*User, error)
	Delete(ctx context.Context, uid string) error
	VerifyCode(ctx context.Context, email, code string) error
	UploadAvatar(ctx context.Context, uid string, file []byte, filename string) (string, error)
}

// UserUsecase 用户用例
type UserUsecase struct {
	repo UserRepo
	log  *log.Helper
}

// NewUserUsecase 创建用户用例
func NewUserUsecase(repo UserRepo, logger log.Logger) *UserUsecase {
	return &UserUsecase{repo: repo, log: log.NewHelper(logger)}
}

// Register 用户注册
func (uc *UserUsecase) Register(ctx context.Context, req *v1.RegisterRequest) (*v1.RegisterReply, error) {
	// 验证验证码
	if err := uc.repo.VerifyCode(ctx, req.Email, req.Code); err != nil {
		return nil, ErrVerificationExpired
	}

	// 检查邮箱是否已存在
	if _, err := uc.repo.FindByEmail(ctx, req.Email); err == nil {
		return nil, ErrEmailAlreadyExists
	}

	// 创建用户
	user := &User{
		UID:       uuid.New().String(),
		Name:      req.Name,
		Email:     req.Email,
		Password:  req.Password, // 密码会在repo层加密
		Avatar:    req.Avatar,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// 如果没有设置头像，使用默认头像
	if user.Avatar == "" {
		user.Avatar = "https://index.knoci.cn/avatar/defuat.jpg"
	}

	// 保存用户
	createdUser, err := uc.repo.Create(ctx, user)
	if err != nil {
		return nil, err
	}

	// 生成token在service层处理
	return &v1.RegisterReply{
		Uid:    createdUser.UID,
		Name:   createdUser.Name,
		Avatar: createdUser.Avatar,
	}, nil
}

// SendVerificationCode 发送验证码
func (uc *UserUsecase) SendVerificationCode(ctx context.Context, email string) (string, error) {
	// 生成6位随机验证码
	code := generateVerificationCode()

	return code, nil
}

// Login 用户登录
func (uc *UserUsecase) Login(ctx context.Context, req *v1.LoginRequest) (*v1.LoginReply, error) {
	// 查找用户
	user, err := uc.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		return nil, ErrUserNotFound
	}

	// 验证密码在repo层处理

	return &v1.LoginReply{
		Uid:    user.UID,
		Name:   user.Name,
		Avatar: user.Avatar,
	}, nil
}

// FindUser 查找用户
func (uc *UserUsecase) FindUser(ctx context.Context, keyword string) (*v1.FindUserReply, error) {
	user, err := uc.repo.FindByKeyword(ctx, keyword)
	if err != nil {
		return nil, ErrUserNotFound
	}

	return &v1.FindUserReply{
		Uid:    user.UID,
		Name:   user.Name,
		Avatar: user.Avatar,
		Email:  user.Email,
	}, nil
}

// DeleteUser 删除用户
func (uc *UserUsecase) DeleteUser(ctx context.Context, uid string) error {
	return uc.repo.Delete(ctx, uid)
}

// UpdateUserInfo 更新用户信息
func (uc *UserUsecase) UpdateUserInfo(ctx context.Context, uid string, req *v1.UpdateUserInfoRequest) (*v1.UpdateUserInfoReply, error) {
	// 验证验证码
	if err := uc.repo.VerifyCode(ctx, req.NewEmail, req.Code); err != nil {
		return nil, ErrVerificationExpired
	}

	// 获取用户
	user, err := uc.repo.FindByUID(ctx, uid)
	if err != nil {
		return nil, ErrUserNotFound
	}

	// 更新用户信息
	user.Name = req.NewName
	user.Email = req.NewEmail
	user.UpdatedAt = time.Now()

	// 保存更新
	updatedUser, err := uc.repo.Update(ctx, user)
	if err != nil {
		return nil, err
	}

	return &v1.UpdateUserInfoReply{
		Uid:   updatedUser.UID,
		Name:  updatedUser.Name,
		Email: updatedUser.Email,
	}, nil
}

// ResetPassword 重置密码
func (uc *UserUsecase) ResetPassword(ctx context.Context, req *v1.ResetPasswordRequest) error {
	// 验证验证码
	if err := uc.repo.VerifyCode(ctx, req.Email, req.Code); err != nil {
		return ErrVerificationExpired
	}

	// 查找用户
	user, err := uc.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		return ErrUserNotFound
	}

	// 更新密码
	user.Password = req.NewPassword
	user.UpdatedAt = time.Now()

	// 保存更新
	_, err = uc.repo.Update(ctx, user)
	return err
}

// UploadAvatar 上传头像
func (uc *UserUsecase) UploadAvatar(ctx context.Context, uid string, fileBytes []byte, filename string) (*v1.UploadAvatarReply, error) {
	// 查找用户
	user, err := uc.repo.FindByUID(ctx, uid)
	if err != nil {
		return nil, ErrUserNotFound
	}

	// 上传头像
	avatarURL, err := uc.repo.UploadAvatar(ctx, uid, fileBytes, filename)
	if err != nil {
		return nil, err
	}

	// 更新用户头像
	user.Avatar = avatarURL
	user.UpdatedAt = time.Now()

	// 保存更新
	updatedUser, err := uc.repo.Update(ctx, user)
	if err != nil {
		return nil, err
	}

	return &v1.UploadAvatarReply{
		Uid:    updatedUser.UID,
		Name:   updatedUser.Name,
		Avatar: updatedUser.Avatar,
	}, nil
}

// 生成6位随机验证码
func generateVerificationCode() string {
	// 实际实现会在repo层
	return "123456"
}
