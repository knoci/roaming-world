package data

import (
	"context"
	"fmt"
	"time"

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
	result := r.data.db.Where("uid = ?", uid).First(&u)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("get user by id error: %v", result.Error)
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

// FindByEmail 根据邮箱获取用户
func (r *userRepo) FindByEmail(ctx context.Context, email string) (*biz.User, error) {
	var u User
	result := r.data.db.Where("email = ?", email).First(&u)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("get user by email error: %v", result.Error)
		return nil, result.Error
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
	result := r.data.db.Where("name LIKE ? OR email LIKE ?", "%"+keyword+"%", "%"+keyword+"%").First(&u)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("find user by keyword error: %v", result.Error)
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

// Update 更新用户信息
func (r *userRepo) Update(ctx context.Context, user *biz.User) (*biz.User, error) {
	var u User
	result := r.data.db.Where("uid = ?", user.UID).First(&u)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("get user by id error: %v", result.Error)
		return nil, result.Error
	}

	u.Name = user.Name
	u.Email = user.Email
	if user.Avatar != "" {
		u.Avatar = user.Avatar
	}

	result = r.data.db.Save(&u)
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("update user error: %v", result.Error)
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

// Delete 删除用户
func (r *userRepo) Delete(ctx context.Context, uid string) error {
	result := r.data.db.Where("uid = ?", uid).Delete(&User{})
	if result.Error != nil {
		r.log.WithContext(ctx).Errorf("delete user error: %v", result.Error)
		return result.Error
	}

	return nil
}

// VerifyCode 验证验证码
func (r *userRepo) VerifyCode(ctx context.Context, email, code string) error {

	return nil
}

// UploadAvatar 上传头像
func (r *userRepo) UploadAvatar(ctx context.Context, uid string, file []byte, filename string) (string, error) {
	// 这里应该实现文件上传逻辑，暂时返回一个模拟的URL
	avatarURL := fmt.Sprintf("https://example.com/avatars/%s/%s", uid, filename)

	// 更新用户头像
	var u User
	result := r.data.db.Where("uid = ?", uid).First(&u)
	if result.Error != nil {
		return "", result.Error
	}

	u.Avatar = avatarURL
	r.data.db.Save(&u)

	return avatarURL, nil
}
