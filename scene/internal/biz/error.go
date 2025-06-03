package biz

import (
	"github.com/go-kratos/kratos/v2/errors"
	v1 "github.com/knoci/roaming-world/scene/api/scene/v1"
)

var (
	// ErrSceneNotFound 场景不存在
	ErrSceneNotFound = errors.NotFound(v1.ErrorReason_SCENE_NOT_FOUND.String(), "scene not found")
	// ErrCreateSceneFailed 创建场景失败
	ErrCreateSceneFailed = errors.InternalServer(v1.ErrorReason_CREATE_SCENE_FAILED.String(), "create scene failed")
	// ErrUpdateSceneFailed 更新场景失败
	ErrUpdateSceneFailed = errors.InternalServer(v1.ErrorReason_UPDATE_SCENE_FAILED.String(), "update scene failed")
	// ErrDeleteSceneFailed 删除场景失败
	ErrDeleteSceneFailed = errors.InternalServer(v1.ErrorReason_DELETE_SCENE_FAILED.String(), "delete scene failed")
	// ErrSearchSceneFailed 搜索场景失败
	ErrSearchSceneFailed = errors.InternalServer(v1.ErrorReason_SEARCH_SCENE_FAILED.String(), "search scene failed")
	// ErrDatabaseError 数据库错误
	ErrDatabaseError = errors.InternalServer(v1.ErrorReason_DATABASE_ERROR.String(), "database error")
	// ErrInvalidArgument 参数错误
	ErrInvalidArgument = errors.BadRequest(v1.ErrorReason_INVALID_ARGUMENT.String(), "invalid argument")
	// ErrUnauthorized 未授权访问
	ErrUnauthorized = errors.Unauthorized(v1.ErrorReason_UNAUTHORIZED.String(), "unauthorized")
)
