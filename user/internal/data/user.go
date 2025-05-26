package data

import (
	"context"
	"errors" 
	"fmt"
	"time"
	"mime/multipart"
	"math/rand"
	"path/filepath"

	"github.com/knoci/roaming-world/user/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// User 用户模型
type User struct {
	UID       string    `gorm:"primaryKey;type:varchar(36);column:uid" json:"uid"`
	Name      string    `gorm:"type:varchar(50);not null" json:"name"`
	Avatar    string    `gorm:"type:varchar(100)" json:"avatar"`
	Password  string    `gorm:"type:text;not null" json:"-"`
	Email     string    `gorm:"type:varchar(20);not null;unique" json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.UID == "" {
		u.UID = uuid.New().String()
	}
	return nil
}

type userRepo struct {
	data *Data
	log  *log.Helper
}

// NewUserRepo 创建用户仓库实例
func NewUserRepo(data *Data, logger log.Logger) biz.UserRepo {
	return &userRepo{
		data: data,
		log:  log.NewHelper(logger),
	}
}

// Create 创建用户
func (r *userRepo) Create(ctx context.Context, user *biz.User) (*biz.User, error) {
	hashPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		r.log.WithContext(ctx).Errorf("hash password error: %v", err)
		return nil, err
	}

	u := &User{
		UID:      user.UID,
		Name:     user.Name,
		Email:    user.Email,
		Password: string(hashPassword),
		Avatar:   user.Avatar,
	}

	result := r.data.db.Create(u)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("create user error: %v", result.Error)
		return nil, result.Error
	}

	return &biz.User{
		UID:       u.UID,
		Name:      u.Name,
		Email:     u.Email,
		Avatar:    u.Avatar,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}, nil
}

// FindByUID 根据ID获取用户
func (r *userRepo) FindByUID(ctx context.Context, uid string) (*biz.User, error) {
	var u User
	result := r.data.db.WithContext(ctx).Where("uid = ?", uid).First(&u)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("user not found by uid: %s", uid)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("db error finding user by uid: %s, error: %v", uid, result.Error)
		return nil, biz.ErrInternalError
	}

	return &biz.User{
		UID:       u.UID,
		Name:      u.Name,
		Email:     u.Email,
		Avatar:    u.Avatar,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}, nil
}

// FindByEmail 根据邮箱获取用户
func (r *userRepo) FindByEmail(ctx context.Context, email string) (*biz.User, error) {
	var u User
	result := r.data.db.WithContext(ctx).Where("email = ?", email).First(&u)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("user not found by email: %s", email)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("db error finding user by email: %s, error: %v", email, result.Error)
		return nil, biz.ErrInternalError
	}

	return &biz.User{
		UID:       u.UID,
		Name:      u.Name,
		Email:     u.Email,
		Password:  u.Password,
		Avatar:    u.Avatar,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}, nil
}

// FindByKeyword 根据关键词查找用户
func (r *userRepo) FindByKeyword(ctx context.Context, keyword string) (*biz.User, error) {
	var u User
	result := r.data.db.WithContext(ctx).Where("name = ? OR email = ?", keyword, keyword).First(&u)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("user not found by keyword: %s", keyword)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("db error finding user by keyword: %s, error: %v", keyword, result.Error)
		return nil, biz.ErrInternalError
	}

	return &biz.User{
		UID:       u.UID,
		Name:      u.Name,
		Email:     u.Email,
		Avatar:    u.Avatar,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}, nil
}

// Update 更新用户信息
func (r *userRepo) Update(ctx context.Context, user *biz.User) (*biz.User, error) {
	// 首先查找用户以确保其存在
	var existingUser User
	findResult := r.data.db.WithContext(ctx).Where("uid = ?", user.UID).First(&existingUser)
	if findResult.Error != nil {
		if errors.Is(findResult.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("user not found for update, uid: %s", user.UID)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("db error finding user for update, uid: %s, error: %v", user.UID, findResult.Error)
		return nil, biz.ErrInternalError
	}

	// 更新字段
	existingUser.Name = user.Name
	existingUser.Email = user.Email
	if user.Avatar != "" {
		existingUser.Avatar = user.Avatar
	}
	existingUser.UpdatedAt = time.Now() // 确保更新时间被设置

	saveResult := r.data.db.WithContext(ctx).Save(&existingUser)
	if saveResult.Error != nil {
		r.log.WithContext(ctx).Errorf("db error updating user, uid: %s, error: %v", user.UID, saveResult.Error)
		return nil, biz.ErrInternalError
	}

	return &biz.User{
		UID:       existingUser.UID,
		Name:      existingUser.Name,
		Email:     existingUser.Email,
		Avatar:    existingUser.Avatar,
		CreatedAt: existingUser.CreatedAt,
		UpdatedAt: existingUser.UpdatedAt,
	}, nil
}

// Delete 删除用户
func (r *userRepo) Delete(ctx context.Context, uid string) error {
	result := r.data.db.WithContext(ctx).Where("uid = ?", uid).Delete(&User{})
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("db error deleting user, uid: %s, error: %v", uid, result.Error)
		return biz.ErrInternalError
	}
	if result.RowsAffected == 0 {
		r.log.WithContext(ctx).Warnf("user not found for deletion, uid: %s", uid)
		return biz.ErrUserNotFound // 如果记录不存在，也应该告知
	}

	r.log.WithContext(ctx).Infof("user deleted successfully, uid: %s, rows_affected: %d", uid, result.RowsAffected)
	return nil
}

// VerifyCode 验证验证码
func (r *userRepo) VerifyCode(ctx context.Context, email, code string) error {
	key := fmt.Sprintf("verify_code:%s:%s", email, code)
	resp, err := r.data.etcd.Get(ctx, key)
	if err != nil {
		r.log.WithContext(ctx).Errorf("etcd get verify code error: %v, key: %s", err, key)
		return fmt.Errorf("failed to get verification code from store: %w", err)
	}
	if len(resp.Kvs) == 0 {
		r.log.WithContext(ctx).Warnf("verify code not found or expired, key: %s", key)
		return biz.ErrVerificationExpired // 使用 biz 层定义的错误
	}

	// 验证成功后删除验证码
	_, err = r.data.etcd.Delete(ctx, key)
	if err != nil {
		// 即使删除失败，验证也已通过，记录错误但不必返回给用户
		r.log.WithContext(ctx).Errorf("etcd delete verify code error: %v, key: %s", err, key)
	}
	r.log.WithContext(ctx).Infof("verify code success and deleted, key: %s", key)
	return nil
}

// UploadAvatar 上传头像并更新用户记录中的头像URL
func (r *userRepo) UploadAvatar(ctx context.Context, uid string, file *multipart.FileHeader) (*biz.User, error) {
	var u User
	findResult := r.data.db.WithContext(ctx).Where("uid = ?", uid).First(&u)
	if findResult.Error != nil {
		if errors.Is(findResult.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("user not found for update, uid: %s", uid)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("db error finding user for update, uid: %s, error: %v", uid, findResult.Error)
		return nil, biz.ErrInternalError
	}

	// 打开文件
	src, err := file.Open()
	if err != nil {
		r.log.WithContext(ctx).Errorf("failed to open avatar file: %s", err)
		return nil, biz.ErrInternalError
	}
	defer src.Close()

	// 生成唯一的文件名
	ext := filepath.Ext(file.Filename)
	code := randcode()
	fileName := fmt.Sprintf("avatars/%s/%s%s", u.Name, code, ext)
	r.log.WithContext(ctx).Infof("starting avatar upload for user: %s, filename: %s", uid, fileName)

	// 上传到腾讯云COS
	_, err = r.data.cos.Object.Put(ctx, fileName, src, nil)
	if err != nil {
		r.log.WithContext(ctx).Errorf("upload avatar to COS error for user %s: %v", uid, err)
		return nil, biz.ErrInternalError
	}

	// 获取文件URL
	avatarURL := r.data.cos.Object.GetObjectURL(fileName)
	r.log.WithContext(ctx).Infof("avatar uploaded for user %s, URL: %s", uid, avatarURL)

	// 更新用户头像URL
	oldAvatar := u.Avatar
	u.Avatar = avatarURL.String()
	u.UpdatedAt = time.Now()

	// 更新用户头像URL
	saveResult := r.data.db.WithContext(ctx).Save(&u)
	if saveResult.Error != nil {
		r.log.WithContext(ctx).Errorf("db error saving user avatar, uid: %s, error: %v", uid, saveResult.Error)
		return nil, biz.ErrInternalError
	}

	// 尝试删除旧头像（如果不是默认头像）
	if oldAvatar != "" && oldAvatar != "https://index.knoci.cn/avatar/defuat.jpg" {
		go func(avatarURL string) {
			_, err := r.data.cos.Object.Delete(ctx, avatarURL)
			if err != nil {
				r.log.WithContext(ctx).Errorf("delete oldAvatar failed: %s", err)
			}
		}(oldAvatar)
	}

	r.log.WithContext(ctx).Infof("user avatar updated in db for user: %s, new avatar URL: %s", uid, avatarURL)
	return &biz.User{
		UID:       u.UID,
		Name:      u.Name,
		Email:     u.Email,
		Avatar:    u.Avatar,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}, nil
}

func (r *userRepo) SetCode(ctx context.Context, key string, time int64) error {
	err := r.data.SetEtcd(ctx, key, time)
	if err != nil {
		r.log.WithContext(ctx).Errorf("save verify code failed: %s", err)
		return err
	}
	return nil
}

func randcode() string {
	return fmt.Sprintf("%12v", rand.New(rand.NewSource(time.Now().UnixNano())).Int31n(1000000))
}