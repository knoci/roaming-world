package data

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	nethttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/knoci/roaming-world/user/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tencentyun/cos-go-sdk-v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var allowedAvatarMIMEToExt = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

func normalizeEmail(email string) string {
	return strings.TrimSpace(strings.ToLower(email))
}

func mapUniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		detail := strings.ToLower(pgErr.ConstraintName + " " + pgErr.Detail + " " + pgErr.Message)
		switch {
		case strings.Contains(detail, "email"):
			return biz.ErrEmailAlreadyExists
		case strings.Contains(detail, "name"):
			return biz.ErrUsernameAlreadyExists
		}
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "email") {
			return biz.ErrEmailAlreadyExists
		}
		if strings.Contains(msg, "name") {
			return biz.ErrUsernameAlreadyExists
		}
	}
	return nil
}

func extractCOSObjectKey(rawURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Path == "" {
		return "", false
	}
	key := strings.TrimPrefix(parsed.Path, "/")
	if key == "" {
		return "", false
	}
	return key, true
}

func detectAvatarType(r io.Reader) (string, string, error) {
	header := make([]byte, 512)
	n, err := io.ReadFull(r, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", biz.ErrInvalidArgument
	}
	if n == 0 {
		return "", "", biz.ErrInvalidArgument
	}
	detectedType := nethttp.DetectContentType(header[:n])
	ext, ok := allowedAvatarMIMEToExt[detectedType]
	if !ok {
		return "", "", biz.ErrInvalidArgument
	}
	return detectedType, ext, nil
}

// User 用户模型
type User struct {
	UID          string    `gorm:"primaryKey;type:varchar(36);column:uid" json:"uid"`
	Name         string    `gorm:"type:varchar(50);not null;uniqueIndex:uk_users_name" json:"name"`
	Avatar       string    `gorm:"type:varchar(255)" json:"avatar"`
	Password     string    `gorm:"type:text;not null" json:"-"`
	Email        string    `gorm:"type:varchar(100);not null;uniqueIndex:uk_users_email" json:"email"`
	Role         string    `gorm:"type:varchar(20);not null;default:user;index:idx_users_role" json:"role"`
	Status       string    `gorm:"type:varchar(20);not null;default:active;index:idx_users_status" json:"status"`
	TokenVersion int64     `gorm:"not null;default:1;column:token_version" json:"token_version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.UID == "" {
		u.UID = uuid.New().String()
	}
	if u.TokenVersion <= 0 {
		u.TokenVersion = 1
	}
	return nil
}

func toBizUser(u *User, includePassword bool) *biz.User {
	ver := u.TokenVersion
	if ver <= 0 {
		ver = 1
	}
	res := &biz.User{
		UID:          u.UID,
		Name:         u.Name,
		Email:        u.Email,
		Avatar:       u.Avatar,
		Role:         u.Role,
		Status:       u.Status,
		TokenVersion: ver,
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
	}
	if includePassword {
		res.Password = u.Password
	}
	return res
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

	tokenVersion := user.TokenVersion
	if tokenVersion <= 0 {
		tokenVersion = 1
	}

	u := &User{
		UID:          user.UID,
		Name:         user.Name,
		Email:        normalizeEmail(user.Email),
		Password:     string(hashPassword),
		Avatar:       user.Avatar,
		Role:         "user",
		Status:       "active",
		TokenVersion: tokenVersion,
	}

	result := r.data.db.WithContext(ctx).Create(u)
	if result.Error != nil {
		if mapped := mapUniqueViolation(result.Error); mapped != nil {
			return nil, mapped
		}
		r.log.WithContext(ctx).Errorf("userRepo: create user error: %v", result.Error)
		if logErr := r.data.LogError(ctx, "user", result.Error.Error(), "db.Create", map[string]string{"uid": u.UID, "name": u.Name}); logErr != nil {
			r.log.WithContext(ctx).Errorf("userRepo: log error failed: %v", logErr)
		}
		return nil, biz.ErrInternalError
	}

	sql := `INSERT INTO users (uid, name, email, password, avatar) VALUES ($1, $2, $3, $4, $5)`
	params := []any{u.UID, u.Name, u.Email, "[REDACTED]", u.Avatar}
	if logErr := r.data.LogSQLAudit(ctx, "user", sql, params); logErr != nil {
		r.log.WithContext(ctx).Errorf("userRepo: sql audit failed: %v", logErr)
	}

	return toBizUser(u, false), nil
}

// FindByUID 根据ID获取用户
func (r *userRepo) FindByUID(ctx context.Context, uid string) (*biz.User, error) {
	var u User
	result := r.data.db.WithContext(ctx).Where("uid = ?", uid).First(&u)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("userRepo: user not found by uid: %s", uid)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user by uid: %s, error: %v", uid, result.Error)
		if err := r.data.LogError(ctx, "user", result.Error.Error(), "db.Where('uid = ?', uid).First", uid); err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: log error failed: %v", err)
		}
		return nil, result.Error
	}

	return toBizUser(&u, false), nil
}

// FindByEmail 根据邮箱获取用户
func (r *userRepo) FindByEmail(ctx context.Context, email string) (*biz.User, error) {
	email = normalizeEmail(email)
	var u User
	result := r.data.db.WithContext(ctx).Where("email = ?", email).First(&u)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("userRepo: user not found by email: %s", email)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user by email: %s, error: %v", email, result.Error)
		if err := r.data.LogError(ctx, "user", result.Error.Error(), "db.Where('email = ?', email).First", email); err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: log error failed: %v", err)
		}
		return nil, result.Error
	}

	return toBizUser(&u, true), nil
}

// FindByName 根据用户名查找用户
func (r *userRepo) FindByName(ctx context.Context, name string) (*biz.User, error) {
	var u User
	result := r.data.db.WithContext(ctx).Where("name = ?", name).First(&u)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("userRepo: user not found by name: %s", name)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user by name: %s, error: %v", name, result.Error)
		_ = r.data.LogError(ctx, "user", result.Error.Error(), "db.Where('name = ?', name).First", name)
		return nil, result.Error
	}
	return toBizUser(&u, false), nil
}

// FindByKeyword 根据关键词查找用户
func (r *userRepo) FindByKeyword(ctx context.Context, keyword string) (*biz.User, error) {
	keyword = strings.TrimSpace(keyword)
	normalizedEmail := normalizeEmail(keyword)
	var u User
	result := r.data.db.WithContext(ctx).Where("name = ? OR email = ? OR uid = ?", keyword, normalizedEmail, keyword).First(&u)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("userRepo: user not found by keyword: %s", keyword)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user by keyword: %s, error: %v", keyword, result.Error)
		if err := r.data.LogError(ctx, "user", result.Error.Error(), "db.Where('name = ? OR email = ?', keyword, keyword).First", keyword); err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: log error failed: %v", err)
		}
		return nil, result.Error
	}

	return toBizUser(&u, false), nil
}

// Update 更新用户信息
func (r *userRepo) Update(ctx context.Context, user *biz.User) (*biz.User, error) {
	// 首先查找用户以确保其存在
	var existingUser User
	findResult := r.data.db.WithContext(ctx).Where("uid = ?", user.UID).First(&existingUser)
	if findResult.Error != nil {
		if errors.Is(findResult.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("userRepo: user not found for update, uid: %s", user.UID)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user for update, uid: %s, error: %v", user.UID, findResult.Error)
		if err := r.data.LogError(ctx, "user", findResult.Error.Error(), "Where('uid = ?', user.UID).First", user.UID); err != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", err)
		}
		return nil, findResult.Error
	}

	// 更新字段。资料更新不会携带密码，避免把已有密码哈希覆盖为空。
	existingUser.Name = user.Name
	existingUser.Email = normalizeEmail(user.Email)
	if user.Password != "" {
		existingUser.Password = user.Password
	}
	if user.Avatar != "" {
		existingUser.Avatar = user.Avatar
	}
	existingUser.UpdatedAt = time.Now()

	saveResult := r.data.db.WithContext(ctx).Save(&existingUser)
	if saveResult.Error != nil {
		if mapped := mapUniqueViolation(saveResult.Error); mapped != nil {
			return nil, mapped
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error updating user, uid: %s, error: %v", user.UID, saveResult.Error)
		return nil, biz.ErrInternalError
	}

	sql := "UPDATE users SET name = $1, email = $2, password = $3, avatar = $4, updated_at = NOW() WHERE uid = $5"
	params := []any{
		existingUser.Name,
		existingUser.Email,
		"[REDACTED]",
		existingUser.Avatar,
		existingUser.UID,
	}
	if err := r.data.LogSQLAudit(ctx, "user", sql, params); err != nil {
		r.log.WithContext(ctx).Errorf("userRepo: kafka send sqllog error: %v", err)
	}

	return toBizUser(&existingUser, false), nil
}

// UpdatePasswordAndBumpVersion atomically updates password hash and increments token_version.
func (r *userRepo) UpdatePasswordAndBumpVersion(ctx context.Context, uid, passwordHash string) (int64, error) {
	var newVersion int64
	res := r.data.db.WithContext(ctx).Raw(
		"UPDATE users SET password = ?, token_version = token_version + 1, updated_at = ? WHERE uid = ? RETURNING token_version",
		passwordHash, time.Now(), uid,
	).Scan(&newVersion)
	if res.Error != nil {
		r.log.WithContext(ctx).Errorf("userRepo: db error updating password and token_version, uid: %s, error: %v", uid, res.Error)
		_ = r.data.LogError(ctx, "user", res.Error.Error(), "UpdatePasswordAndBumpVersion", uid)
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, biz.ErrUserNotFound
	}
	return newVersion, nil
}

// IncrementTokenVersion atomically increments token_version for a user and returns the new version.
func (r *userRepo) IncrementTokenVersion(ctx context.Context, uid string) (int64, error) {
	var newVersion int64
	res := r.data.db.WithContext(ctx).Raw(
		"UPDATE users SET token_version = token_version + 1, updated_at = ? WHERE uid = ? RETURNING token_version",
		time.Now(), uid,
	).Scan(&newVersion)
	if res.Error != nil {
		r.log.WithContext(ctx).Errorf("userRepo: db error incrementing token_version, uid: %s, error: %v", uid, res.Error)
		_ = r.data.LogError(ctx, "user", res.Error.Error(), "IncrementTokenVersion", uid)
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, biz.ErrUserNotFound
	}
	return newVersion, nil
}

// Delete removes follows, notifications, and the user record in a single transaction,
// returning the bumped token version to revoke all active sessions.
func (r *userRepo) Delete(ctx context.Context, uid string) (int64, error) {
	var newVersion int64
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("follower_uid = ? OR followee_uid = ?", uid, uid).Delete(&Follow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("uid = ?", uid).Delete(&Notification{}).Error; err != nil {
			return err
		}
		var oldVersion int64
		res := tx.Raw("DELETE FROM users WHERE uid = ? RETURNING token_version", uid).Scan(&oldVersion)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if oldVersion < 1 {
			oldVersion = 1
		}
		newVersion = oldVersion + 1
		return nil
	})
	if err != nil {
		r.log.WithContext(ctx).Errorf("userRepo: db error deleting user, uid: %s, error: %v", uid, err)
		if logErr := r.data.LogError(ctx, "user", err.Error(), "Transaction.Delete", uid); logErr != nil {
			r.log.WithContext(ctx).Errorf("userRepo: log error failed: %v", logErr)
		}
		return 0, err
	}
	if newVersion == 0 {
		r.log.WithContext(ctx).Warnf("userRepo: user not found for deletion, uid: %s", uid)
		return 0, nil
	}
	sql := "DELETE FROM users WHERE uid = $1"
	if logErr := r.data.LogSQLAudit(ctx, "user", sql, []any{uid}); logErr != nil {
		r.log.WithContext(ctx).Errorf("userRepo: sql audit failed: %v", logErr)
	}
	r.log.WithContext(ctx).Infof("userRepo: user deleted successfully, uid: %s, revoked_version: %d", uid, newVersion)
	return newVersion, nil
}

// UploadAvatar 上传头像并更新用户记录中的头像URL
func (r *userRepo) UploadAvatar(ctx context.Context, uid string, file *multipart.FileHeader) (*biz.User, error) {
	var u User
	findResult := r.data.db.WithContext(ctx).Where("uid = ?", uid).First(&u)
	if findResult.Error != nil {
		if errors.Is(findResult.Error, gorm.ErrRecordNotFound) {
			r.log.WithContext(ctx).Warnf("userRepo: user not found for update, uid: %s", uid)
			return nil, biz.ErrUserNotFound
		}
		r.log.WithContext(ctx).Errorf("userRepo: db error finding user for update, uid: %s, error: %v", uid, findResult.Error)
		if logErr := r.data.LogError(ctx, "user", findResult.Error.Error(), "db.Where(uid = ?, uid)", uid); logErr != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", logErr)
		}
		return nil, findResult.Error
	}

	// 打开文件
	src, err := file.Open()
	if err != nil {
		r.log.WithContext(ctx).Errorf("failed to open avatar file: %s", err)
		if logErr := r.data.LogError(ctx, "user", err.Error(), "file.Open", uid); logErr != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", logErr)
		}
		return nil, err
	}
	defer src.Close()

	// 魔数校验并严格根据白名单 MIME 派生扩展名
	detectedType, ext, err := detectAvatarType(src)
	if err != nil {
		return nil, biz.ErrInvalidArgument
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		r.log.WithContext(ctx).Errorf("failed to seek avatar file: %v", err)
		return nil, err
	}

	// 生成安全的唯一 COS 对象键（使用 uid + uuid + 校验后的扩展名）
	fileName := fmt.Sprintf("avatars/%s/%s%s", u.UID, uuid.NewString(), ext)
	r.log.WithContext(ctx).Infof("userRepo: starting avatar upload for user: %s, key: %s", uid, fileName)

	// 上传到腾讯云COS
	putOpts := &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
			ContentType: detectedType,
		},
	}
	_, err = r.data.cos.Object.Put(ctx, fileName, src, putOpts)
	if err != nil {
		r.log.WithContext(ctx).Errorf("userRepo: upload avatar to COS error for user %s: %v", uid, err)
		if logErr := r.data.LogError(ctx, "user", err.Error(), "cos.Object.Put", map[string]string{"uid": u.UID, "key": fileName}); logErr != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", logErr)
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
		if logErr := r.data.LogError(ctx, "user", saveResult.Error.Error(), "db.Save", map[string]string{"uid": u.UID, "name": u.Name}); logErr != nil {
			r.log.WithContext(ctx).Errorf("userRepo: kafka send errorlog error: %v", logErr)
		}
		return nil, saveResult.Error
	}

	// 发送消息到Kafka
	sql := "UPDATE users SET avatar = $1, updated_at = NOW() WHERE uid = $2"
	params := []any{
		avatarURL.String(),
		u.UID,
	}
	if err := r.data.LogSQLAudit(ctx, "user", sql, params); err != nil {
		r.log.WithContext(ctx).Errorf("userRepo: kafka send sqllog error: %v", err)
	}

	// 尝试异步删除旧头像（如果不是默认头像），使用 WithoutCancel 防止请求结束后被取消
	if oldAvatar != "" && oldAvatar != "https://index.knoci.cn/avatar/defuat.jpg" {
		if oldKey, ok := extractCOSObjectKey(oldAvatar); ok {
			baseCtx := context.WithoutCancel(ctx)
			go func(bgCtx context.Context, objectKey string) {
				delCtx, cancel := context.WithTimeout(bgCtx, 10*time.Second)
				defer cancel()
				_, err := r.data.cos.Object.Delete(delCtx, objectKey)
				if err != nil {
					r.log.WithContext(delCtx).Errorf("userRepo: delete oldAvatar failed: %s", err)
					if logErr := r.data.LogError(delCtx, "user", err.Error(), "cos.Object.Delete", objectKey); logErr != nil {
						r.log.WithContext(delCtx).Errorf("userRepo: kafka send errorlog error: %v", logErr)
					}
				}
			}(baseCtx, oldKey)
		}
	}

	r.log.WithContext(ctx).Infof("userRepo: user avatar updated in db for user: %s, new avatar URL: %s", uid, avatarURL)
	return toBizUser(&u, false), nil
}
