package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"
	"travel-world/initialize"
	"travel-world/model"
	"travel-world/pkg/email"
	"travel-world/pkg/jwt"

	"github.com/tencentyun/cos-go-sdk-v5"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type UserService struct{}

type VerifyEmailRequest struct {
	Email string `json:"email" binding:"required,email"`
	Code  string `json:"code" binding:"required,len=6"`
}

type UpdateUserInfoRequest struct {
	NewName  string `json:"newname" binding:"required,min=2,max=20"`
	NewEmail string `json:"newemail" binding:"required,email"`
	Code     string `json:"code" binding:"required,len=6"`
}

type RegisterRequest struct {
	Name     string `json:"name" binding:"required,min=2,max=20"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6,max=20"`
	Code     string `json:"code" binding:"len=6"`
	Avatar   string `json:"avatar"`
}

type VerifyRequest struct {
	Email string `json:"email" binding:"required,email"`
	Code  string `json:"code" binding:"required,len=6"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type UserResponse struct {
	UID    string `json:"uid"`
	Name   string `json:"name"`
	Token  string `json:"token,omitempty"`
	Avatar string `json:"avatar,omitempty"`
	Email  string `json:"email,omitempty"`
}

// AvatarUploadResponse 头像上传响应
type AvatarUploadResponse struct {
	UID    string `json:"uid"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

// VerifyEmail 验证用户邮箱
func (s *UserService) VerifyEmail(uid string, req *VerifyEmailRequest) error {
	// 验证验证码
	ctx := context.Background()
	key := fmt.Sprintf("verify_code:%s:%s", req.Email, req.Code)

	// 从etcd获取验证码
	resp, err := initialize.EtcdClient.Get(ctx, key)
	if err != nil {
		return errors.New("获取验证失败")
	}

	if len(resp.Kvs) == 0 {
		return errors.New("验证码已过期或不存在")
	}

	// 验证成功后删除验证码
	_, err = initialize.EtcdClient.Delete(ctx, key)
	if err != nil {
		initialize.Logger.Error("Failed to Delete code in etcd", zap.Error(err))
	}

	return nil
}

// UpdateUserInfo 更新用户信息
func (s *UserService) UpdateUserInfo(uid string, req *UpdateUserInfoRequest) (*UserResponse, error) {
	// 验证验证码
	ctx := context.Background()
	key := fmt.Sprintf("verify_code:%s:%s", req.NewEmail, req.Code)

	// 从etcd获取验证码
	resp, err := initialize.EtcdClient.Get(ctx, key)
	if err != nil {
		return nil, errors.New("获取验证码失败")
	}

	if len(resp.Kvs) == 0 {
		return nil, errors.New("验证码已过期或不存在")
	}

	// 验证成功后删除验证码
	_, err = initialize.EtcdClient.Delete(ctx, key)
	if err != nil {
		initialize.Logger.Error("Failed to Delete code in etcd", zap.Error(err))
	}

	// 检查新用户名是否已存在
	var existUser model.User
	if err := initialize.DB.Where("name = ? AND uid != ?", req.NewName, uid).First(&existUser).Error; err == nil {
		return nil, errors.New("用户名已存在")
	}

	// 检查新邮箱是否已存在
	if err := initialize.DB.Where("email = ? AND uid != ?", req.NewEmail, uid).First(&existUser).Error; err == nil {
		return nil, errors.New("邮箱已被注册")
	}

	// 更新用户信息
	result := initialize.DB.Model(&model.User{}).Where("uid = ?", uid).Updates(map[string]interface{}{
		"name":  req.NewName,
		"email": req.NewEmail,
	})

	if result.Error != nil {
		return nil, result.Error
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("UPDATE users SET name = '%s', email = '%s' WHERE uid = '%s'", req.NewName, req.NewEmail, uid); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	return &UserResponse{
		UID:   uid,
		Name:  req.NewName,
		Email: req.NewEmail,
	}, nil
}

// Register 用户注册
func (s *UserService) Register(req *RegisterRequest) (*UserResponse, error) {
	// 验证验证码
	ctx := context.Background()
	key := fmt.Sprintf("verify_code:%s:%s", req.Email, req.Code)

	// 从etcd获取验证码
	resp, err := initialize.EtcdClient.Get(ctx, key)
	if err != nil {
		return nil, errors.New("获取验证码失败")
	}

	if len(resp.Kvs) == 0 {
		return nil, errors.New("验证码已过期或不存在")
	}

	// 检查用户名是否已存在
	var existUser model.User
	if err := initialize.DB.Where("name = ?", req.Name).First(&existUser).Error; err == nil {
		return nil, errors.New("用户名已存在")
	}

	// 检查邮箱是否已存在
	if err := initialize.DB.Where("email = ?", req.Email).First(&existUser).Error; err == nil {
		return nil, errors.New("邮箱已被注册")
	}

	// 验证成功后删除验证码
	_, err = initialize.EtcdClient.Delete(ctx, key)
	if err != nil {
		initialize.Logger.Error("Failed to Delete code in etcd", zap.Error(err))
	}

	// 密码加密
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	if req.Avatar == "" {
		req.Avatar = "https://index.knoci.cn/avatar/defuat.jpg"
	}

	// 创建用户
	user := &model.User{
		Name:     req.Name,
		Email:    req.Email,
		Password: string(hashedPassword),
		Avatar:   req.Avatar,
	}

	// 记录SQL操作并执行
	if err := initialize.DB.Create(user).Error; err != nil {
		return nil, err
	}

	if err := initialize.SendSQLLog("INSERT INTO users (uid, name, email, password, avatar) VALUES ('%s', '%s', '%s', '%s', '%s')",
		user.UID, user.Name, user.Email, user.Password, user.Avatar); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 生成 token
	token, err := jwt.GenerateToken(user.UID, user.Name, user.Avatar, user.Email)
	if err != nil {
		return nil, err
	}

	return &UserResponse{
		UID:    user.UID,
		Name:   user.Name,
		Token:  token,
		Avatar: user.Avatar,
	}, nil
}

// VerifyAndCompleteRegistration 生成验证码并存储到Etcd
func (s *UserService) VerifyAndCompleteRegistration(req *RegisterRequest) (string, error) {
	ctx := context.Background()
	// 生成6位随机验证码
	code := generateVerificationCode()

	// 将验证码存储到Etcd，设置10分钟过期
	key := fmt.Sprintf("verify_code:%s:%s", req.Email, code)
	lease, err := initialize.EtcdClient.Grant(ctx, 600) // 10分钟 = 600秒
	if err != nil {
		return "", errors.New("创建租约失败")
	}

	// 使用租约存储验证码，值设为1
	_, err = initialize.EtcdClient.Put(ctx, key, "1", clientv3.WithLease(lease.ID))
	if err != nil {
		return "", errors.New("存储验证码失败")
	}
	// 发送验证码邮件
	if err := email.SendVerificationCode(req.Email, code); err != nil {
		return "", errors.New("发送验证码失败：" + err.Error())
	}

	return code, nil
}

// generateVerificationCode 生成6位随机验证码
func generateVerificationCode() string {
	rand.Seed(time.Now().UnixNano())
	code := rand.Intn(900000) + 100000 // 生成100000-999999之间的随机数
	return fmt.Sprintf("%06d", code)
}

// Login 用户登录
func (s *UserService) Login(req *LoginRequest) (*UserResponse, error) {
	// 查找用户
	var user model.User
	if err := initialize.DB.Where("email = ?", req.Email).First(&user).Error; err != nil {
		return nil, errors.New("用户不存在")
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		return nil, errors.New("密码错误")
	}

	// 生成 token
	token, err := jwt.GenerateToken(user.UID, user.Name, user.Avatar, user.Email)
	if err != nil {
		return nil, err
	}

	return &UserResponse{
		UID:    user.UID,
		Name:   user.Name,
		Avatar: user.Avatar,
		Token:  token,
	}, nil
}

// FindByKeyword 根据关键字查找用户
func (s *UserService) FindByKeyword(keyword string) (*UserResponse, error) {
	var user model.User
	if err := initialize.DB.Where("uid = ? OR name = ?", keyword, keyword).First(&user).Error; err != nil {
		return nil, errors.New("用户不存在")
	}

	return &UserResponse{
		UID:    user.UID,
		Name:   user.Name,
		Avatar: user.Avatar,
		Email:  user.Email,
	}, nil
}

// DeleteUser 删除用户
func (s *UserService) DeleteUser(uid string) error {
	// 删除用户
	result := initialize.DB.Where("uid = ?", uid).Delete(&model.User{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("用户不存在")
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("DELETE FROM users WHERE uid = '%s'", uid); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	return nil
}

type CheckUserExistRequest struct {
	Name  string `json:"name" binding:"required"`
	Email string `json:"email" binding:"required,email"`
}

// CheckUserExist 检查用户名和邮箱是否存在
func (s *UserService) CheckUserExist(req *CheckUserExistRequest) error {
	// 在数据库中查找用户
	var user model.User
	result := initialize.DB.Where("name = ? AND email = ?", req.Name, req.Email).First(&user)

	return result.Error
}

type ResetPasswordRequest struct {
	Name        string `json:"name" binding:"required,min=2,max=20"`
	Email       string `json:"email" binding:"required,email"`
	Code        string `json:"code" binding:"required,len=6"`
	NewPassword string `json:"newpassword" binding:"required,min=6,max=20"`
}

// ResetPassword 通过验证码重置密码
func (s *UserService) ResetPassword(req *ResetPasswordRequest) error {
	// 验证验证码
	ctx := context.Background()
	key := fmt.Sprintf("verify_code:%s:%s", req.Email, req.Code)

	// 从etcd获取验证码
	resp, err := initialize.EtcdClient.Get(ctx, key)
	if err != nil {
		return errors.New("获取验证码失败")
	}

	if len(resp.Kvs) == 0 {
		return errors.New("验证码已过期或不存在")
	}

	// 验证成功后删除验证码
	_, err = initialize.EtcdClient.Delete(ctx, key)
	if err != nil {
		initialize.Logger.Error("Failed to Delete code in etcd", zap.Error(err))
	}

	// 更新用户密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// 更新数据库中的密码
	result := initialize.DB.Model(&model.User{}).Where("email = ?", req.Email).Update("password", string(hashedPassword))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("用户不存在")
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("UPDATE users SET password = '%s' WHERE email = '%s'", string(hashedPassword), req.Email); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	return nil
}

// UploadAvatar 上传用户头像到腾讯云COS
func (s *UserService) UploadAvatar(uid string, file *multipart.FileHeader) (*AvatarUploadResponse, error) {
	// 检查用户是否存在
	var user model.User
	if err := initialize.DB.Where("uid = ?", uid).First(&user).Error; err != nil {
		return nil, errors.New("用户不存在")
	}

	// 打开文件
	src, err := file.Open()
	if err != nil {
		initialize.Logger.Error("打开文件失败", zap.Error(err))
		return nil, errors.New("打开文件失败")
	}
	defer src.Close()

	// 生成唯一的文件名
	ext := filepath.Ext(file.Filename)
	code := generateVerificationCode()
	fileName := fmt.Sprintf("avatars/%s/%s%s", user.Name, code, ext)

	// 上传到腾讯云COS
	_, err = initialize.COSClient.Object.Put(context.Background(), fileName, src, nil)
	if err != nil {
		initialize.Logger.Error("上传到腾讯云COS失败", zap.Error(err))
		return nil, errors.New("上传头像失败")
	}

	// 获取文件URL
	avatarURL := initialize.COSClient.Object.GetObjectURL(fileName)
	if avatarURL == nil || avatarURL.String() == "" {
		initialize.Logger.Error("获取文件URL失败", zap.Error(err))
		return nil, errors.New("获取头像URL失败")
	}

	// 更新用户头像URL
	oldAvatar := user.Avatar
	user.Avatar = avatarURL.String()
	user.UpdatedAt = time.Now()

	// 更新数据库
	if err := initialize.DB.Model(&user).Update("avatar", user.Avatar).Error; err != nil {
		initialize.Logger.Error("更新用户头像失败", zap.Error(err))
		return nil, errors.New("更新用户头像失败")
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("UPDATE users SET avatar = '%s' WHERE uid = '%s'", user.Avatar, uid); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 尝试删除旧头像（如果不是默认头像）
	if oldAvatar != "" && oldAvatar != "https://travel-1304888063.cos.ap-guangzhou.myqcloud.com/defuat%20avatar.jpg" {
		// 从URL中提取对象键
		// 注意：这里需要根据实际URL格式进行调整
		// 这里假设oldAvatar是完整的URL，需要提取出对象键
		// 实际实现可能需要根据URL格式进行调整
		go func(avatarURL string) {
			// 异步删除旧头像，避免影响主流程
			// 这里需要根据实际URL格式提取对象键
			// 简化处理，实际可能需要更复杂的URL解析
			ctx := context.Background()
			_, err := initialize.COSClient.Object.Delete(ctx, avatarURL)
			if err != nil {
				initialize.Logger.Warn("删除旧头像失败", zap.String("url", avatarURL), zap.Error(err))
			}
		}(oldAvatar)
	}

	return &AvatarUploadResponse{
		UID:    user.UID,
		Name:   user.Name,
		Avatar: user.Avatar,
	}, nil
}

// GetObjectURL 获取对象的URL
func (s *UserService) GetObjectURL(objectKey string) (string, error) {
	url := initialize.COSClient.Object.GetObjectURL(objectKey)
	return url.String(), nil
}

type MultiPostRequest struct {
	Files []*multipart.FileHeader `form:"files" binding:"required,min=1"`
}

type MultiPostResponse struct {
	URLs []string `json:"urls"`
}

// MultiPost 处理多文件上传
func (s *UserService) MultiPost(files []*multipart.FileHeader) (*MultiPostResponse, error) {
	urls := make([]string, 0, len(files))
	ctx := context.Background()

	for _, file := range files {
		// 检查文件大小（500MB = 500 * 1024 * 1024 字节）
		if file.Size > 500*1024*1024 {
			return nil, fmt.Errorf("文件 %s 超过500MB限制", file.Filename)
		}

		// 获取文件扩展名
		ext := strings.ToLower(filepath.Ext(file.Filename))
		// 检查文件类型
		if !isAllowedFileType(ext) {
			return nil, fmt.Errorf("不支持的文件类型: %s", ext)
		}

		// 打开文件
		src, err := file.Open()
		if err != nil {
			return nil, err
		}

		// 计算文件哈希
		hash := sha256.New()
		if _, err := io.Copy(hash, src); err != nil {
			src.Close()
			return nil, fmt.Errorf("计算文件哈希失败: %v", err)
		}
		fileHash := hex.EncodeToString(hash.Sum(nil))

		// 检查Redis中是否存在该文件的URL
		fileKey := fmt.Sprintf("file:hash:%s", fileHash)
		if url, err := initialize.RDB.Get(ctx, fileKey).Result(); err == nil {
			// 文件已存在，直接使用缓存的URL
			urls = append(urls, url)
			src.Close()
			continue
		}

		// 重置文件指针到开始位置
		if _, err := src.Seek(0, 0); err != nil {
			src.Close()
			return nil, fmt.Errorf("重置文件指针失败: %v", err)
		}

		// 生成唯一文件名
		uniqueName := fmt.Sprintf("/source/%s%s", fileHash, ext)

		// 上传到COS
		opt := &cos.ObjectPutOptions{
			ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
				ContentType: file.Header.Get("Content-Type"),
			},
		}

		_, err = initialize.COSClient.Object.Put(context.Background(), uniqueName, src, opt)
		src.Close()
		if err != nil {
			return nil, fmt.Errorf("上传文件失败: %v", err)
		}

		// 获取文件URL
		url := initialize.COSClient.Object.GetObjectURL(uniqueName).String()
		urls = append(urls, url)

		// 将文件哈希和URL的映射保存到Redis，设置六个月过期时间
		if err := initialize.RDB.Set(ctx, fileKey, url, 180*24*time.Hour).Err(); err != nil {
			initialize.Logger.Warn("保存文件哈希到Redis失败", zap.Error(err))
		}
	}

	return &MultiPostResponse{URLs: urls}, nil
}

// isAllowedFileType 检查文件类型是否允许
func isAllowedFileType(ext string) bool {
	allowedTypes := map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".gif":  true,
		".mp4":  true,
		".mov":  true,
		".avi":  true,
		".wmv":  true,
	}
	return allowedTypes[ext]
}

// UpdateAvatar 更新文章和评论中的用户头像
func (s *UserService) UpdateAvatar() {
	// 缓存用户头像信息和存在状态
	userAvatars := make(map[string]string)
	userExists := make(map[string]bool)

	// 批量获取所有用户的头像信息
	var users []model.User
	if err := initialize.DB.Select("uid, avatar").Find(&users).Error; err != nil {
		initialize.Logger.Error("获取用户列表失败", zap.Error(err))
		return
	}

	// 构建用户头像缓存和存在状态
	for _, user := range users {
		userAvatars[user.UID] = user.Avatar
		userExists[user.UID] = true
	}

	// 分批处理文章
	go s.updateArticleAvatars(userAvatars, userExists)
	// 分批处理评论
	go s.updateCommentAvatars(userAvatars, userExists)

	initialize.Logger.Info("Started avatar update process")
}

// updateArticleAvatars 异步更新文章头像
func (s *UserService) updateArticleAvatars(userAvatars map[string]string, userExists map[string]bool) {
	batchSize := 300
	offset := 0

	for {
		// 开始事务
		tx := initialize.DB.Begin()

		var articles []model.Article
		if err := tx.Select("aid, uid, avatar").Limit(batchSize).Offset(offset).Find(&articles).Error; err != nil {
			tx.Rollback()
			initialize.Logger.Error("获取文章批次失败", zap.Error(err))
			break
		}

		if len(articles) == 0 {
			tx.Rollback()
			break
		}

		// 收集需要删除的文章和需要更新的文章
		deleteAIDs := make([]string, 0)
		updateArticles := make(map[string]string)

		for _, article := range articles {
			if exists := userExists[article.UID]; !exists {
				deleteAIDs = append(deleteAIDs, article.AID)
			} else if newAvatar, exists := userAvatars[article.UID]; exists && article.Avatar != newAvatar {
				updateArticles[article.AID] = newAvatar
			}
		}

		// 批量删除文章
		if len(deleteAIDs) > 0 {
			if err := tx.Where("aid IN (?)", deleteAIDs).Delete(&model.Article{}).Error; err != nil {
				tx.Rollback()
				initialize.Logger.Error("批量删除文章失败", zap.Error(err))
				continue
			}
			// 发送SQL日志到Kafka
			for _, aid := range deleteAIDs {
				if err := initialize.SendSQLLog("DELETE FROM articles WHERE aid = '%s'", aid); err != nil {
					initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
				}
			}
		}

		// 批量更新文章头像
		if len(updateArticles) > 0 {
			for aid, newAvatar := range updateArticles {
				if err := tx.Model(&model.Article{}).Where("aid = ?", aid).Update("avatar", newAvatar).Error; err != nil {
					initialize.Logger.Error("更新文章作者头像失败", zap.String("aid", aid), zap.Error(err))
					continue
				}
				// 发送SQL日志到Kafka
				if err := initialize.SendSQLLog("UPDATE articles SET avatar = '%s' WHERE aid = '%s'", newAvatar, aid); err != nil {
					initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
				}
			}
		}

		// 提交事务
		if err := tx.Commit().Error; err != nil {
			tx.Rollback()
			initialize.Logger.Error("提交文章更新事务失败", zap.Error(err))
			continue
		}

		offset += batchSize
	}
}

// updateCommentAvatars 异步更新评论头像
func (s *UserService) updateCommentAvatars(userAvatars map[string]string, userExists map[string]bool) {
	batchSize := 300
	offset := 0

	for {
		// 开始事务
		tx := initialize.DB.Begin()

		var comments []model.Comment
		if err := tx.Select("cid, uid, avatar, target").Limit(batchSize).Offset(offset).Find(&comments).Error; err != nil {
			tx.Rollback()
			initialize.Logger.Error("获取评论批次失败", zap.Error(err))
			break
		}

		if len(comments) == 0 {
			tx.Rollback()
			break
		}

		// 收集需要删除的评论和需要更新的评论
		deleteCIDs := make([]string, 0)
		updateComments := make(map[string]string)
		commentTargets := make(map[string]string) // 存储评论对应的文章ID

		for _, comment := range comments {
			if exists := userExists[comment.UID]; !exists {
				deleteCIDs = append(deleteCIDs, comment.CID)
				commentTargets[comment.CID] = comment.Target
			} else if newAvatar, exists := userAvatars[comment.UID]; exists && comment.Avatar != newAvatar {
				updateComments[comment.CID] = newAvatar
			}
		}

		// 批量删除评论
		if len(deleteCIDs) > 0 {
			if err := tx.Where("cid IN (?)", deleteCIDs).Delete(&model.Comment{}).Error; err != nil {
				tx.Rollback()
				initialize.Logger.Error("批量删除评论失败", zap.Error(err))
				continue
			}

			// 批量更新文章评论数
			for _, cid := range deleteCIDs {
				target := commentTargets[cid]
				if err := tx.Model(&model.Article{}).Where("aid = ?", target).UpdateColumn("comments", gorm.Expr("comments - ?", 1)).Error; err != nil {
					initialize.Logger.Error("更新文章评论数失败", zap.String("aid", target), zap.Error(err))
				}

				// 更新Redis中的文章评论数
				if err := initialize.RDB.ZIncrBy(context.Background(), "article:comments", -1, target).Err(); err != nil {
					initialize.Logger.Error("更新Redis文章评论数失败", zap.String("aid", target), zap.Error(err))
				}

				// 发送SQL日志到Kafka
				if err := initialize.SendSQLLog("DELETE FROM comments WHERE cid = '%s'", cid); err != nil {
					initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
				}
				if err := initialize.SendSQLLog("UPDATE articles SET comments = comments - 1 WHERE aid = '%s'", target); err != nil {
					initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
				}
			}
		}

		// 批量更新评论头像
		if len(updateComments) > 0 {
			for cid, newAvatar := range updateComments {
				if err := tx.Model(&model.Comment{}).Where("cid = ?", cid).Update("avatar", newAvatar).Error; err != nil {
					initialize.Logger.Error("更新评论作者头像失败", zap.String("cid", cid), zap.Error(err))
					continue
				}
				// 发送SQL日志到Kafka
				if err := initialize.SendSQLLog("UPDATE comments SET avatar = '%s' WHERE cid = '%s'", newAvatar, cid); err != nil {
					initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
				}
			}
		}

		// 提交事务
		if err := tx.Commit().Error; err != nil {
			tx.Rollback()
			initialize.Logger.Error("提交评论更新事务失败", zap.Error(err))
			continue
		}

		offset += batchSize
	}
}
