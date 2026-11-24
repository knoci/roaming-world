package biz

import (
	"github.com/go-kratos/kratos/v2/errors"
	v1 "github.com/knoci/roaming-world/audiobook/api/audiobook/v1"
)

var (
	// ErrAudiobookNotFound 有声书不存在
	ErrAudiobookNotFound = errors.NotFound(v1.ErrorReason_AUDIOBOOK_NOT_FOUND.String(), "audiobook not found")
	// ErrDetailNotFound 章节不存在
	ErrDetailNotFound = errors.NotFound(v1.ErrorReason_DETAIL_NOT_FOUND.String(), "detail not found")
	// ErrCreateAudiobookFailed 创建有声书失败
	ErrCreateAudiobookFailed = errors.InternalServer(v1.ErrorReason_CREATE_AUDIOBOOK_FAILED.String(), "create audiobook failed")
	// ErrCreateDetailFailed 创建章节失败
	ErrCreateDetailFailed = errors.InternalServer(v1.ErrorReason_CREATE_DETAIL_FAILED.String(), "create detail failed")
	// ErrDatabaseError 数据库错误
	ErrDatabaseError = errors.InternalServer(v1.ErrorReason_DATABASE_ERROR.String(), "database error")
	// ErrUnauthorized 未授权访问
	ErrUnauthorized = errors.Unauthorized(v1.ErrorReason_UNAUTHORIZED.String(), "unauthorized")
	// ErrForbidden 禁止访问
	ErrForbidden = errors.Forbidden("FORBIDDEN", "forbidden")
)
