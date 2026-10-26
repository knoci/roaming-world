package biz

import (
	v1 "github.com/knoci/roaming-world/article/api/article/v1"

	"github.com/go-kratos/kratos/v2/errors"
)

var (
	ErrArticleNotFound     = errors.NotFound(v1.ErrorReason_ARTICLE_NOT_FOUND.String(), "article not found")
	ErrInvalidArgument     = errors.BadRequest(v1.ErrorReason_INVALID_ARGUMENT.String(), "invalid argument")
	ErrUnauthorized        = errors.Unauthorized(v1.ErrorReason_UNAUTHORIZED.String(), "unauthorized")
	ErrNoPermission        = errors.Forbidden(v1.ErrorReason_NO_PERMISSION.String(), "no permission")
	ErrInternalError       = errors.InternalServer(v1.ErrorReason_INTERNAL_ERROR.String(), "internal error")
	ErrCreateArticleFailed = errors.InternalServer(v1.ErrorReason_INTERNAL_ERROR.String(), "create article failed")
	ErrUpdateArticleFailed = errors.InternalServer(v1.ErrorReason_INTERNAL_ERROR.String(), "update article failed")
	ErrDeleteArticleFailed = errors.InternalServer(v1.ErrorReason_INTERNAL_ERROR.String(), "delete article failed")
)
