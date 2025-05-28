package biz

import (
	"github.com/go-kratos/kratos/v2/errors"
	v1 "github.com/knoci/roaming-world/food/api/food/v1"
)

var (
	// 错误定义
	ErrUserNotFound          = errors.NotFound(v1.ErrorReason_NOT_FOUND.String(), "不存在")
	ErrInvalidArgument       = errors.BadRequest(v1.ErrorReason_INVALID_ARGUMENT.String(), "请求参数错误")
	ErrEmailAlreadyExists    = errors.Conflict(v1.ErrorReason_EMAIL_ALREADY_EXISTS.String(), "邮箱已被注册")
	ErrUsernameAlreadyExists = errors.Conflict(v1.ErrorReason_USERNAME_ALREADY_EXISTS.String(), "用户名已存在")
	ErrVerificationExpired   = errors.BadRequest(v1.ErrorReason_VERIFICATION_CODE_EXPIRED.String(), "验证码已过期或不存在")
	ErrIncorrectPassword     = errors.Unauthorized(v1.ErrorReason_INCORRECT_PASSWORD.String(), "密码错误")
	ErrUnauthorized          = errors.Unauthorized(v1.ErrorReason_UNAUTHORIZED.String(), "未授权访问")
	ErrInternalError         = errors.InternalServer(v1.ErrorReason_INTERNAL_ERROR.String(), "服务器内部错误")
)
