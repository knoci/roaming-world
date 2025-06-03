package biz

import "github.com/go-kratos/kratos/v2/errors"

// 错误码定义
var (
	// ErrUserNotFound 用户不存在
	ErrUserNotFound = errors.New(404, "USER_NOT_FOUND", "用户不存在")

	// ErrCommentNotFound 评论不存在
	ErrCommentNotFound = errors.New(404, "COMMENT_NOT_FOUND", "评论不存在")

	// ErrNoPermission 无权限
	ErrNoPermission = errors.New(403, "NO_PERMISSION", "无权限操作")

	// ErrInternalError 内部错误
	ErrInternalError = errors.New(500, "INTERNAL_ERROR", "内部错误")

	// ErrUnauthorized 未授权
	ErrUnauthorized = errors.New(401, "UNAUTHORIZED", "未登录")

	// ErrParentCommentNotFound 父评论不存在
	ErrParentCommentNotFound = errors.New(404, "PARENT_COMMENT_NOT_FOUND", "回复的评论不存在")
)
