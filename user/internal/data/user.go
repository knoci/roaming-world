package data

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"mime/multipart"
	"path/filepath"
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
		r.log.WithContext(ctx).Errorf("userRepo: hash password error: %v", err)
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
		r.log.WithContext(ctx).Errorf("userRepo: create user error: %v", result.Error)
		err = r.data.SendErrorLog(ctx, "user", result.Error.Error(), "db.Create", u)
		if err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", err)
		}
		return nil, result.Error
	}

	sql := `INSERT INTO users (uid, name, email, password, avatar) VALUES ($1, $2, $3, $4, $5)`
	params := []any{user.UID, user.Name, user.Email, string(hashPassword), user.Avatar}
	err = r.data.SendSqlLog(ctx, "user", sql, params)
	if err != nil {
		r.log.WithContext(ctx).Errorf("userRepo: kafka send sqllog error: %v", err)
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
			r.log.WithContext(ctx).Warnf("userRepo: user not found by uid: %s", uid)
			return nil, result.Error
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user by uid: %s, error: %v", uid, result.Error)
		err := r.data.SendErrorLog(ctx, "user", result.Error.Error(), "db.Where('uid = ?', uid).First", uid)
		if err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", err)
		}
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
	result := r.data.db.WithContext(ctx).Where("email = ?", email).First(&u)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("userRepo: user not found by email: %s", email)
			return nil, result.Error
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user by email: %s, error: %v", email, result.Error)
		err := r.data.SendErrorLog(ctx, "user", result.Error.Error(), "db.Where('email = ?', email).First", email)
		if err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", err)
		}
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
	result := r.data.db.WithContext(ctx).Where("name = ? OR email = ?", keyword, keyword).First(&u)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("userRepo: user not found by keyword: %s", keyword)
			return nil, result.Error
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user by keyword: %s, error: %v", keyword, result.Error)
		err := r.data.SendErrorLog(ctx, "user", result.Error.Error(), "db.Where('name = ? OR email = ?', keyword, keyword).First", keyword)
		if err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", err)
		}
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
	// 首先查找用户以确保其存在
	var existingUser User
	findResult := r.data.db.WithContext(ctx).Where("uid = ?", user.UID).First(&existingUser)
	if findResult.Error != nil {
		if errors.Is(findResult.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("userRepo: user not found for update, uid: %s", user.UID)
			return nil, findResult.Error
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user for update, uid: %s, error: %v", user.UID, findResult.Error)
		err := r.data.SendErrorLog(ctx, "user", findResult.Error.Error(), "Where('uid = ?', user.UID).First", user.UID)
		if err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", err)
		}
		return nil, findResult.Error
	}

	// 更新字段
	existingUser.Name = user.Name
	existingUser.Email = user.Email
	existingUser.Password = user.Password
	existingUser.Avatar = user.Avatar
	existingUser.UpdatedAt = time.Now() // 确保更新时间被设置
	if user.Avatar != "" {
		existingUser.Avatar = user.Avatar
	}
	existingUser.UpdatedAt = time.Now() // 确保更新时间被设置

	saveResult := r.data.db.WithContext(ctx).Save(&existingUser)
	if saveResult.Error != nil {
		r.log.WithContext(ctx).Errorf("userRepo: db error updating user, uid: %s, error: %v", user.UID, saveResult.Error)
		return nil, biz.ErrInternalError
	}

	sql := "UPDATE users SET name = $1, email = $2, password = $3, avatar = $4, updated_at = NOW() WHERE id = $5"
	params := []any{
		user.Name,
		user.Email,
		user.Password,
		user.Avatar,
		existingUser.UID,
	}
	err := r.data.SendSqlLog(ctx, "user", sql, params)
	if err != nil {
		r.log.WithContext(ctx).Errorf("userRepo: kafka send sqllog error: %v", err)
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
		r.log.WithContext(ctx).Errorf("userRepo: db error deleting user, uid: %s, error: %v", uid, result.Error)
		err := r.data.SendErrorLog(ctx, "user", result.Error.Error(), "Where('uid = ?', uid).Delete", uid)
		if err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", err)
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		r.log.WithContext(ctx).Warnf("userRepo: user not found for deletion, uid: %s", uid)
		return nil
	} else {
		sql := "DELETE FROM users WHERE uid = $1"
		params := []any{uid}
		err := r.data.SendSqlLog(ctx, "user", sql, params)
		if err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send sqllog error: %v", err)
		}
	}

	r.log.WithContext(ctx).Infof("userRepo: user deleted successfully, uid: %s, rows_affected: %d", uid, result.RowsAffected)
	return nil
}

// VerifyCode 验证验证码
func (r *userRepo) VerifyCode(ctx context.Context, email, code string) error {
	key := fmt.Sprintf("verify_code:%s:%s", email, code)
	resp, err := r.data.etcd.Get(ctx, key)
	if err != nil {
		r.log.WithContext(ctx).Errorf("userRepo: etcd get verify code error: %v, key: %s", err, key)
		error := r.data.SendErrorLog(ctx, "user", err.Error(), "etcd.Get", key)
		if error != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", error)
		}
		return err
	}
	if len(resp.Kvs) == 0 {
		return err
	}

	// 验证成功后删除验证码
	_, err = r.data.etcd.Delete(ctx, key)
	if err != nil {
		// 即使删除失败，验证也已通过，记录错误但不必返回给用户
		r.log.WithContext(ctx).Errorf("userRepo: etcd delete verify code error: %v, key: %s", err, key)
	}
	r.log.WithContext(ctx).Infof("userRepo: verify code success and deleted, key: %s", key)
	return nil
}

// UploadAvatar 上传头像并更新用户记录中的头像URL
func (r *userRepo) UploadAvatar(ctx context.Context, uid string, file *multipart.FileHeader) (*biz.User, error) {
	var u User
	findResult := r.data.db.WithContext(ctx).Where("uid = ?", uid).First(&u)
	if findResult.Error != nil {
		if errors.Is(findResult.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("userRepo: user not found for update, uid: %s", uid)
			return nil, findResult.Error
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user for update, uid: %s, error: %v", uid, findResult.Error)
		error := r.data.SendErrorLog(ctx, "user", findResult.Error.Error(), "db.Where(uid = ?, uid)", uid)
		if error != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", error)
		}
		return nil, findResult.Error
	}

	// 打开文件
	src, err := file.Open()
	if err != nil {
		r.log.WithContext(ctx).Errorf("failed to open avatar file: %s", err)
		error := r.data.SendErrorLog(ctx, "user", err.Error(), "file.Open", file)
		if error != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", error)
		}
		return nil, err
	}
	defer src.Close()

	// 生成唯一的文件名
	ext := filepath.Ext(file.Filename)
	code := randcode()
	fileName := fmt.Sprintf("avatars/%s/%s%s", u.Name, code, ext)
	r.log.WithContext(ctx).Infof("userRepo: starting avatar upload for userRepo: %s, filename: %s", uid, fileName)

	// 上传到腾讯云COS
	_, err = r.data.cos.Object.Put(ctx, fileName, src, nil)
	if err != nil {
		r.log.WithContext(ctx).Errorf("userRepo: upload avatar to COS error for user %s: %v", uid, err)
		error := r.data.SendErrorLog(ctx, "user", err.Error(), "cos.Object.Put", src)
		if error != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", error)
		}
		return nil, err
	}

	// 获取文件URL
	avatarURL := r.data.cos.Object.GetObjectURL(fileName)
	r.log.WithContext(ctx).Infof("userRepo: avatar uploaded for user %s, URL: %s", uid, avatarURL)

	// 更新用户头像URL
	oldAvatar := u.Avatar
	u.Avatar = avatarURL.String()
	u.UpdatedAt = time.Now()

	// 更新用户头像URL
	saveResult := r.data.db.WithContext(ctx).Save(&u)
	if saveResult.Error != nil {
		r.log.WithContext(ctx).Errorf("userRepo: db error saving user avatar, uid: %s, error: %v", uid, saveResult.Error)
		error := r.data.SendErrorLog(ctx, "user", saveResult.Error.Error(), "db.Save", u)
		if error != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", error)
		}
		return nil, saveResult.Error
	}

	// 发送消息到Kafka
	sql := "UPDATE users SET avatar = $1, updated_at = NOW() WHERE id = $2"
	params := []any{
		avatarURL.String(),
		u.UID,
	}
	err = r.data.SendSqlLog(ctx, "user", sql, params)
	if err != nil {
		r.log.WithContext(ctx).Errorf("userRepo: kafka send sqllog error: %v", err)
	}

	// 尝试删除旧头像（如果不是默认头像）
	if oldAvatar != "" && oldAvatar != "https://index.knoci.cn/avatar/defuat.jpg" {
		go func(avatarURL string) {
			_, err := r.data.cos.Object.Delete(ctx, avatarURL)
			if err != nil {
				r.log.WithContext(ctx).Errorf("userRepo: delete oldAvatar failed: %s", err)
				error := r.data.SendErrorLog(ctx, "user", err.Error(), "cos.Object.Delete", avatarURL)
				if error != nil {
					r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", error)
				}
			}
		}(oldAvatar)
	}

	r.log.WithContext(ctx).Infof("userRepo: user avatar updated in db for userRepo: %s, new avatar URL: %s", uid, avatarURL)
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
		r.log.WithContext(ctx).Errorf("userRepo: save verify code failed: %s", err)
		return err
	}
	return nil
}

func randcode() string {
	return fmt.Sprintf("%12v", rand.New(rand.NewSource(time.Now().UnixNano())).Int31n(1000000))
}
