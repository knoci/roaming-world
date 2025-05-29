package biz

import (
	"context"
	"fmt"
	"math/rand"
	"mime/multipart"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	v1 "github.com/knoci/roaming-world/user/api/user/v1"
	"github.com/knoci/roaming-world/user/internal/pkg"
	"golang.org/x/crypto/bcrypt"
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
	SetCode(ctx context.Context, key string, time int64) error
	UploadAvatar(ctx context.Context, uid string, file *multipart.FileHeader) (*User, error)
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

	return &v1.RegisterReply{
		Uid:    createdUser.UID,
		Name:   createdUser.Name,
		Avatar: createdUser.Avatar,
	}, nil
}

// SendVerificationCode 发送验证码并存储到etcd
func (uc *UserUsecase) SendVerificationCode(ctx context.Context, email string) (string, error) {
	// 生成6位随机验证码
	code := generateVerificationCode()

	// 将验证码存储到etcd，有效期10分钟
	key := fmt.Sprintf("verify_code:%s:%s", email, code)
	err := uc.repo.SetCode(ctx, key, 600)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("userUsecase: failed to store verification code in etcd: %v", err)
		return "", ErrInternalError
	}

	err = pkg.SendVerificationCode(email, key)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("userUsecase: failed to send code by email: %v", err)
		return "", ErrInternalError
	}

	uc.log.WithContext(ctx).Infof("userUsecase: verification code %s sent to %s and stored with key %s", code, email, key)
	return code, nil
}

// Login 用户登录
func (uc *UserUsecase) Login(ctx context.Context, req *v1.LoginRequest) (*v1.LoginReply, error) {
	// 查找用户
	user, err := uc.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) { // 确保比较的是业务错误
			uc.log.WithContext(ctx).Warnf("userUsecase: login attempt for non-existent email: %s", req.Email)
			return nil, ErrUserNotFound
		}
		uc.log.WithContext(ctx).Errorf("userUsecase: error finding user by email during login: %v", err)
		return nil, ErrInternalError
	}

	// 验证密码
	if err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		return nil, ErrIncorrectPassword
	}

	uc.log.WithContext(ctx).Infof("userUsecase: user %s logged in successfully", user.Email)
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
	err := uc.repo.Delete(ctx, uid)
	if err != nil {
		return ErrInternalError
	}
	return nil
}

// UpdateUserInfo 更新用户信息
func (uc *UserUsecase) UpdateUserInfo(ctx context.Context, uid string, req *v1.UpdateUserInfoRequest) (*v1.UpdateUserInfoReply, error) {
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
	hashPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	user.Password = string(hashPassword)
	user.UpdatedAt = time.Now()

	// 保存更新
	_, err = uc.repo.Update(ctx, user)
	if err != nil {
		return ErrInternalError
	}
	return nil
}

// UploadAvatar 上传头像
func (uc *UserUsecase) UploadAvatar(ctx context.Context, uid string, file *multipart.FileHeader) (*v1.UploadAvatarReply, error) {
	uc.log.WithContext(ctx).Infof("userUsecase: uploading avatar for user %s, filename: %s", uid, file.Filename)

	result, err := uc.repo.UploadAvatar(ctx, uid, file)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("userUsecase: failed to upload avatar for user %s: %v", uid, err)
		return nil, ErrInternalError
	}

	uc.log.WithContext(ctx).Infof("userUsecase: avatar uploaded successfully for user %s, URL: %s", uid, result.Avatar)
	return &v1.UploadAvatarReply{
		Uid:    result.UID,
		Name:   result.Name,
		Avatar: result.Avatar,
	}, nil
}

// ConfirmEmail 验证邮箱
func (uc *UserUsecase) ConfirmEmail(ctx context.Context, req *v1.ConfirmEmailRequest) (*v1.ConfirmEmailReply, error) {
	uc.log.WithContext(ctx).Infof("userUsecase: confirm mail email %s, code: %s", req.Email, req.Code)

	err := uc.repo.VerifyCode(ctx, req.Email, req.Code)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("userUsecase: failed to confirm mail email %s, code: %s", req.Email, req.Code)
		return &v1.ConfirmEmailReply{
			Status: "验证失败",
		}, ErrInternalError
	}

	uc.log.WithContext(ctx).Infof("userUsecase: successfully confirm mail email %s, code: %s", req.Email, req.Code)
	return &v1.ConfirmEmailReply{
		Status: "验证成功",
	}, nil
}

// generateVerificationCode 生成6位随机数字验证码
func generateVerificationCode() string {
	return fmt.Sprintf("%06v", rand.New(rand.NewSource(time.Now().UnixNano())).Int31n(1000000))
}
