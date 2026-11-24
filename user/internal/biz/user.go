package biz

import (
	"context"
	"mime/multipart"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/knoci/roaming-world/pkg/auth"
	v1 "github.com/knoci/roaming-world/user/api/user/v1"
	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordBytes = 6
	maxPasswordBytes = 72
)

func normalizeEmail(email string) string {
	return strings.TrimSpace(strings.ToLower(email))
}

// SessionStore abstracts refresh token storage and access token revocation backed by Redis.
type SessionStore interface {
	auth.RevocationChecker
	SaveRefreshToken(ctx context.Context, uid, rawToken string, ttl time.Duration) error
	ConsumeRefreshToken(ctx context.Context, rawToken string) (string, error)
	RevokeAllUserSessions(ctx context.Context, uid string, newVersion int64) error
	RevokeAccessToken(ctx context.Context, jti string, ttl time.Duration) error
}

// User 用户实体
type User struct {
	UID          string
	Name         string
	Email        string
	Password     string
	Avatar       string
	Role         string
	Status       string
	TokenVersion int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// UserRepo 用户仓库接口
type UserRepo interface {
	Create(ctx context.Context, user *User) (*User, error)
	Update(ctx context.Context, user *User) (*User, error)
	UpdatePasswordAndBumpVersion(ctx context.Context, uid, passwordHash string) (int64, error)
	IncrementTokenVersion(ctx context.Context, uid string) (int64, error)
	FindByUID(ctx context.Context, uid string) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByName(ctx context.Context, name string) (*User, error)
	FindByKeyword(ctx context.Context, keyword string) (*User, error)
	Delete(ctx context.Context, uid string) (int64, error)
	IssueVerificationCode(ctx context.Context, target VerificationTarget, code string) (func(context.Context) error, error)
	CheckVerificationCode(ctx context.Context, target VerificationTarget, code string, consume bool) error
	UploadAvatar(ctx context.Context, uid string, file *multipart.FileHeader) (*User, error)
}

// UserUsecase 用户用例
type UserUsecase struct {
	repo     UserRepo
	sessions SessionStore
	log      *log.Helper
}

// NewUserUsecase 创建用户用例
func NewUserUsecase(repo UserRepo, sessions SessionStore, logger log.Logger) *UserUsecase {
	return &UserUsecase{repo: repo, sessions: sessions, log: log.NewHelper(logger)}
}

// RegisterUser creates a new user and returns the persisted User entity including role and token version.
func (uc *UserUsecase) RegisterUser(ctx context.Context, req *v1.RegisterRequest) (*User, error) {
	email := normalizeEmail(req.Email)
	if len(req.Password) < minPasswordBytes || len(req.Password) > maxPasswordBytes {
		return nil, ErrInvalidArgument
	}

	// 检查用户名是否已存在（在消费验证码之前检查，避免重名浪费有效验证码）
	if _, err := uc.repo.FindByName(ctx, req.Name); err == nil {
		return nil, ErrUsernameAlreadyExists
	} else if !errors.Is(err, ErrUserNotFound) {
		return nil, ErrInternalError
	}

	// 检查邮箱是否已存在（在消费验证码之前检查，避免重复邮箱浪费有效验证码）
	if _, err := uc.repo.FindByEmail(ctx, email); err == nil {
		return nil, ErrEmailAlreadyExists
	} else if !errors.Is(err, ErrUserNotFound) {
		return nil, ErrInternalError
	}

	// 验证并消费验证码
	if err := uc.checkVerificationCode(ctx, VerificationTarget{Email: email, Purpose: VerificationRegister}, req.Code, true); err != nil {
		return nil, err
	}

	// 创建用户
	user := &User{
		Name:         req.Name,
		Email:        email,
		Password:     req.Password, // 密码会在repo层加密
		Avatar:       req.Avatar,
		Role:         "user",
		Status:       "active",
		TokenVersion: 1,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
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
	if createdUser.Role == "" {
		createdUser.Role = "user"
	}
	if createdUser.TokenVersion <= 0 {
		createdUser.TokenVersion = 1
	}
	return createdUser, nil
}

// Register 用户注册
func (uc *UserUsecase) Register(ctx context.Context, req *v1.RegisterRequest) (*v1.RegisterReply, error) {
	createdUser, err := uc.RegisterUser(ctx, req)
	if err != nil {
		return nil, err
	}
	return &v1.RegisterReply{
		Uid:    createdUser.UID,
		Name:   createdUser.Name,
		Avatar: createdUser.Avatar,
	}, nil
}

// Authenticate verifies credentials in a single FindByEmail query and returns the user (including Role and TokenVersion).
func (uc *UserUsecase) Authenticate(ctx context.Context, req *v1.LoginRequest) (*User, error) {
	email := normalizeEmail(req.Email)
	if len(req.Password) > maxPasswordBytes {
		return nil, ErrInvalidCredentials
	}

	// 单次查询读取密码哈希、角色、状态与 TokenVersion
	user, err := uc.repo.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) { // 不暴露邮箱是否存在
			uc.log.WithContext(ctx).Warnf("userUsecase: login attempt for non-existent email: %s", email)
			return nil, ErrInvalidCredentials
		}
		uc.log.WithContext(ctx).Errorf("userUsecase: error finding user by email during login: %v", err)
		return nil, ErrInternalError
	}

	// 验证密码
	if err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	if user.Status == "banned" {
		return nil, ErrUserBanned
	}

	if user.Role == "" {
		user.Role = "user"
	}
	if user.TokenVersion <= 0 {
		user.TokenVersion = 1
	}
	user.Password = ""

	uc.log.WithContext(ctx).Infof("userUsecase: user %s logged in successfully", user.Email)
	return user, nil
}

// Login 用户登录
func (uc *UserUsecase) Login(ctx context.Context, req *v1.LoginRequest) (*v1.LoginReply, error) {
	user, err := uc.Authenticate(ctx, req)
	if err != nil {
		return nil, err
	}
	return &v1.LoginReply{
		Uid:    user.UID,
		Name:   user.Name,
		Avatar: user.Avatar,
	}, nil
}

// FindByUID returns the user record by UID.
func (uc *UserUsecase) FindByUID(ctx context.Context, uid string) (*User, error) {
	user, err := uc.repo.FindByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if user.Role == "" {
		user.Role = "user"
	}
	if user.TokenVersion <= 0 {
		user.TokenVersion = 1
	}
	return user, nil
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
	}, nil
}

// DeleteUser bumps token_version, deletes follows/notifications/user in one transaction, and revokes all active sessions.
func (uc *UserUsecase) DeleteUser(ctx context.Context, uid string) (int64, error) {
	newVersion, err := uc.repo.Delete(ctx, uid)
	if err != nil {
		return 0, ErrInternalError
	}
	if uc.sessions != nil && newVersion > 0 {
		if err := uc.sessions.RevokeAllUserSessions(ctx, uid, newVersion); err != nil {
			uc.log.WithContext(ctx).Errorf("userUsecase: failed to revoke sessions after delete for %s: %v", uid, err)
			return newVersion, ErrInternalError
		}
	}
	return newVersion, nil
}

// BumpTokenVersion increments token_version in DB and revokes all user sessions in Redis.
func (uc *UserUsecase) BumpTokenVersion(ctx context.Context, uid string) (int64, error) {
	newVersion, err := uc.repo.IncrementTokenVersion(ctx, uid)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return 0, ErrUserNotFound
		}
		return 0, ErrInternalError
	}
	if uc.sessions != nil {
		if err := uc.sessions.RevokeAllUserSessions(ctx, uid, newVersion); err != nil {
			uc.log.WithContext(ctx).Errorf("userUsecase: failed to revoke sessions for %s: %v", uid, err)
			return newVersion, ErrInternalError
		}
	}
	return newVersion, nil
}

// UpdateUserInfo 更新用户信息
func (uc *UserUsecase) UpdateUserInfo(ctx context.Context, uid string, req *v1.UpdateUserInfoRequest) (*v1.UpdateUserInfoReply, error) {
	newEmail := normalizeEmail(req.NewEmail)

	// 获取用户
	user, err := uc.repo.FindByUID(ctx, uid)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, ErrInternalError
	}

	// 用户名不能被其他用户占用（在消费验证码之前检查）
	if other, err := uc.repo.FindByName(ctx, req.NewName); err == nil && other.UID != uid {
		return nil, ErrUsernameAlreadyExists
	} else if err != nil && !errors.Is(err, ErrUserNotFound) {
		return nil, ErrInternalError
	}

	// 邮箱不能被其他用户占用（在消费验证码之前检查）
	if other, err := uc.repo.FindByEmail(ctx, newEmail); err == nil && other.UID != uid {
		return nil, ErrEmailAlreadyExists
	} else if err != nil && !errors.Is(err, ErrUserNotFound) {
		return nil, ErrInternalError
	}

	// 校验并消费新邮箱的验证码
	if err := uc.checkVerificationCode(ctx, VerificationTarget{Email: newEmail, Purpose: VerificationChangeEmail, UID: uid}, req.Code, true); err != nil {
		return nil, err
	}

	// 更新用户信息
	user.Name = req.NewName
	user.Email = newEmail
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

// CheckUserExist 注册前检查用户名和邮箱是否匹配（对应 gin 的 /user/confirm）。
func (uc *UserUsecase) CheckUserExist(ctx context.Context, name, email string) error {
	email = normalizeEmail(email)
	user, err := uc.repo.FindByEmail(ctx, email)
	if err != nil {
		return ErrUserNotFound
	}
	if user.Name != name {
		return ErrUserNotFound
	}
	return nil
}

// ResetPassword resets password, bumps token_version = token_version + 1 in DB, and revokes all active sessions.
func (uc *UserUsecase) ResetPassword(ctx context.Context, req *v1.ResetPasswordRequest) error {
	email := normalizeEmail(req.Email)
	if len(req.NewPassword) < minPasswordBytes || len(req.NewPassword) > maxPasswordBytes {
		return ErrInvalidArgument
	}
	// 验证验证码
	if err := uc.checkVerificationCode(ctx, VerificationTarget{Email: email, Purpose: VerificationResetPassword}, req.Code, true); err != nil {
		return err
	}

	// 查找用户
	user, err := uc.repo.FindByEmail(ctx, email)
	if err != nil {
		return ErrUserNotFound
	}
	if req.Name != "" && user.Name != req.Name {
		return ErrUserNotFound
	}

	// 更新密码并原子递增 token_version
	hashPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return ErrInternalError
	}

	newVersion, err := uc.repo.UpdatePasswordAndBumpVersion(ctx, user.UID, string(hashPassword))
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrUserNotFound
		}
		return ErrInternalError
	}
	if uc.sessions != nil {
		if err := uc.sessions.RevokeAllUserSessions(ctx, user.UID, newVersion); err != nil {
			uc.log.WithContext(ctx).Errorf("userUsecase: failed to revoke sessions after password reset for %s: %v", user.UID, err)
			return ErrInternalError
		}
	}
	return nil
}

// UploadAvatar 上传头像
func (uc *UserUsecase) UploadAvatar(ctx context.Context, uid string, file *multipart.FileHeader) (*v1.UploadAvatarReply, error) {
	uc.log.WithContext(ctx).Infof("userUsecase: uploading avatar for user %s, filename: %s", uid, file.Filename)

	result, err := uc.repo.UploadAvatar(ctx, uid, file)
	if err != nil {
		if errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrUserNotFound) {
			return nil, err
		}
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
