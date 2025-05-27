package api

import (
	"net/http"
	"travel-world/service"

	"github.com/gin-gonic/gin"
)

type UserController struct {
	userService service.UserService
}

func NewUserController() *UserController {
	return &UserController{
		userService: service.UserService{},
	}
}

// Register 用户注册
func (ctrl *UserController) Register(c *gin.Context) {
	var req service.RegisterRequest

	// 尝试绑定 JSON
	if err := c.ShouldBindJSON(&req); err != nil {
		// JSON 绑定失败，尝试绑定 URL 查询参数
		if err := c.ShouldBindQuery(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 400,
				"msg":  "请求参数错误: " + err.Error(),
			})
			return
		}
	}

	user, err := ctrl.userService.Register(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "注册成功",
		"data": user,
	})
}

// VerifyAndCompleteRegistration 发送验证码
func (ctrl *UserController) VerifyAndCompleteRegistration(c *gin.Context) {
	var req service.RegisterRequest
	// 从query参数获取邮箱
	email := c.Query("email")
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "邮箱参数不能为空",
		})
		return
	}

	req.Email = email

	code, err := ctrl.userService.VerifyAndCompleteRegistration(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "验证码已发送",
		"data": gin.H{"code": code},
	})
}

// Login 用户登录
func (ctrl *UserController) Login(c *gin.Context) {
	var req service.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误",
		})
		return
	}

	user, err := ctrl.userService.Login(&req)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 401,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "登录成功",
		"data": user,
	})
}

// CheckUserExist 检查用户名和邮箱是否存在
func (ctrl *UserController) CheckUserExist(c *gin.Context) {
	var req service.CheckUserExistRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	err := ctrl.userService.CheckUserExist(&req)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "验证成功",
	})
}

// GetUserInfo 获取用户信息
func (ctrl *UserController) GetUserInfo(c *gin.Context) {
	uid, _ := c.Get("uid")
	name, _ := c.Get("name")

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "success",
		"data": gin.H{
			"uid":  uid,
			"name": name,
		},
	})
}

// Find 根据关键字查找用户
func (ctrl *UserController) Find(c *gin.Context) {
	keyword := c.Query("keyword")
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "缺少参数",
		})
		return
	}

	user, err := ctrl.userService.FindByKeyword(keyword)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "查询成功",
		"data": user,
	})
}

// Delete 删除用户
func (ctrl *UserController) Delete(c *gin.Context) {
	uid, _ := c.Get("uid")
	if uid == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 401,
			"msg":  "未授权访问",
		})
		return
	}

	if err := ctrl.userService.DeleteUser(uid.(string)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "删除成功",
	})
}

// ResetPassword 重置密码
func (ctrl *UserController) ResetPassword(c *gin.Context) {
	var req service.ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	if err := ctrl.userService.ResetPassword(&req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "密码重置成功",
	})
}

// PostAvatar 上传用户头像
func (ctrl *UserController) PostAvatar(c *gin.Context) {
	// 从上下文中获取用户ID
	uid, exists := c.Get("uid")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 401,
			"msg":  "请先登录",
		})
		return
	}

	// 获取上传的文件
	file, err := c.FormFile("avatar")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请选择要上传的头像文件",
		})
		return
	}

	// 检查文件类型
	contentType := file.Header.Get("Content-Type")
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/gif" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "只支持JPG、PNG和GIF格式的图片",
		})
		return
	}

	// 检查文件大小（限制为3MB）
	if file.Size > 3*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "头像文件大小不能超过3MB",
		})
		return
	}

	// 上传头像
	response, err := ctrl.userService.UploadAvatar(uid.(string), file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "头像上传成功",
		"data": response,
	})
}

// MultiPost 处理多文件上传
func (ctrl *UserController) MultiPost(c *gin.Context) {
	// 获取上传的文件
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "获取文件失败: " + err.Error(),
		})
		return
	}

	files := form.File["files"]
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请选择要上传的文件",
		})
		return
	}

	// 调用服务层处理文件上传
	resp, err := ctrl.userService.MultiPost(files)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "上传成功",
		"data": resp,
	})
}

// VerifyEmail 验证用户邮箱
func (ctrl *UserController) VerifyEmail(c *gin.Context) {
	uid, exists := c.Get("uid")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 401,
			"msg":  "未授权访问",
		})
		return
	}

	var req service.VerifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	err := ctrl.userService.VerifyEmail(uid.(string), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "邮箱验证成功",
	})
}

// UpdateUserInfo 更新用户信息
func (ctrl *UserController) UpdateUserInfo(c *gin.Context) {
	uid, exists := c.Get("uid")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 401,
			"msg":  "未授权访问",
		})
		return
	}

	var req service.UpdateUserInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	userInfo, err := ctrl.userService.UpdateUserInfo(uid.(string), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "用户信息更新成功",
		"data": userInfo,
	})
}
