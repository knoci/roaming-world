package middleware

import (
	"net/http"
	"strings"
	"travel-world/pkg/jwt"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware JWT 认证中间件
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取 Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "请先登录",
			})
			c.Abort()
			return
		}

		// 检查 token 格式
		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "无效的token格式",
			})
			c.Abort()
			return
		}

		// 解析 token
		claims, err := jwt.ParseToken(parts[1])
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "无效的token",
			})
			c.Abort()
			return
		}

		// 获取请求中的用户ID
		requestUID := c.Query("uid")
		if requestUID == "" {
			requestUID = c.PostForm("uid")
		}

		// 验证用户身份
		if claims.UID != requestUID {
			c.JSON(http.StatusForbidden, gin.H{
				"code": 403,
				"msg":  "无权操作其他用户的账号",
			})
			c.Abort()
			return
		}

		// 将用户信息存储到上下文
		c.Set("uid", claims.UID)
		c.Set("name", claims.Name)
		c.Set("avatar", claims.Avatar)
		c.Next()
	}
}

// AuthPasswordMiddleware 密码认证中间件
func AuthPasswordMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取 Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "没有权限",
			})
			c.Abort()
			return
		}

		// 检查密码是否匹配
		if authHeader != "knoci1337" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "没有权限",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// AuthUserMiddleware 用户身份认证中间件
func AuthUserMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取 Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "请先登录",
			})
			c.Abort()
			return
		}

		// 检查 token 格式
		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "无效的token格式",
			})
			c.Abort()
			return
		}

		// 解析 token
		claims, err := jwt.ParseToken(parts[1])
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": 401,
				"msg":  "无效的token",
			})
			c.Abort()
			return
		}

		// 将用户信息存储到上下文
		c.Set("uid", claims.UID)
		c.Set("name", claims.Name)
		c.Set("avatar", claims.Avatar)
		c.Set("email", claims.Email)
		c.Next()
	}
}
