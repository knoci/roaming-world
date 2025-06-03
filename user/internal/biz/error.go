package biz

import (
	"github.com/go-kratos/kratos/v2/errors"
	v1 "github.com/knoci/roaming-world/user/api/user/v1"
)

var (
	// 错误定义
	ErrUserNotFound          = errors.NotFound(v1.ErrorReason_NOT_FOUND.String(), "not found")
	ErrInvalidArgument       = errors.BadRequest(v1.ErrorReason_INVALID_ARGUMENT.String(), "invalid argument")
	ErrEmailAlreadyExists    = errors.Conflict(v1.ErrorReason_EMAIL_ALREADY_EXISTS.String(), "email already exists")
	ErrUsernameAlreadyExists = errors.Conflict(v1.ErrorReason_USERNAME_ALREADY_EXISTS.String(), "user name already exists")
	ErrVerificationExpired   = errors.BadRequest(v1.ErrorReason_VERIFICATION_CODE_EXPIRED.String(), "verification code expired")
	ErrIncorrectPassword     = errors.Unauthorized(v1.ErrorReason_INCORRECT_PASSWORD.String(), "incorrect password")
	ErrUnauthorized          = errors.Unauthorized(v1.ErrorReason_UNAUTHORIZED.String(), "unauthorized")
	ErrInternalError         = errors.InternalServer(v1.ErrorReason_INTERNAL_ERROR.String(), "internal error")
)
