package biz

import (
	"context"
	"time"

	v1 "scene/api/scene/v1"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
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
)

// Scene 场景实体
type Scene struct {
	SID       string
	Name      string
	Describe  string
	View      []string
	Location  string
	Article   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SceneRepo 场景仓库接口
type SceneRepo interface {
	CreateScene(ctx context.Context, scene *Scene) (*Scene, error)
	DeleteScene(ctx context.Context, sid string) error
	UpdateScene(ctx context.Context, scene *Scene) (*Scene, error)
	SearchScene(ctx context.Context, keyword string) ([]*Scene, error)
	ListScenes(ctx context.Context, limit int) ([]*Scene, error)
	GetSceneByID(ctx context.Context, sid string) (*Scene, error)
}

// SceneUsecase 场景用例
type SceneUsecase struct {
	repo SceneRepo
	log  *log.Helper
}

// NewSceneUsecase 创建场景用例
func NewSceneUsecase(repo SceneRepo, logger log.Logger) *SceneUsecase {
	return &SceneUsecase{repo: repo, log: log.NewHelper(logger)}
}

// CreateScene 创建场景
func (uc *SceneUsecase) CreateScene(ctx context.Context, req *v1.CreateSceneRequest) (*v1.SceneMessage, error) {
	uc.log.WithContext(ctx).Infof("CreateScene: %v", req.Name)

	scene := &Scene{
		Name:     req.Name,
		Describe: req.Describe,
		View:     req.View,
		Location: req.Location,
		Article:  req.Article,
	}

	createdScene, err := uc.repo.CreateScene(ctx, scene)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("CreateScene failed: %v", err)
		return nil, ErrCreateSceneFailed
	}

	return &v1.SceneMessage{
		Sid:       createdScene.SID,
		Name:      createdScene.Name,
		Describe:  createdScene.Describe,
		View:      createdScene.View,
		Location:  createdScene.Location,
		Article:   createdScene.Article,
		CreatedAt: createdScene.CreatedAt.Format(time.RFC3339),
		UpdatedAt: createdScene.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// DeleteScene 删除场景
func (uc *SceneUsecase) DeleteScene(ctx context.Context, sid string) error {
	uc.log.WithContext(ctx).Infof("DeleteScene: SID=%s", sid)

	err := uc.repo.DeleteScene(ctx, sid)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("DeleteScene failed: %v", err)
		return ErrDeleteSceneFailed
	}

	return nil
}

// UpdateScene 更新场景
func (uc *SceneUsecase) UpdateScene(ctx context.Context, req *v1.UpdateSceneRequest) (*v1.SceneMessage, error) {
	uc.log.WithContext(ctx).Infof("UpdateScene: SID=%s", req.Sid)

	// 检查场景是否存在
	existingScene, err := uc.repo.GetSceneByID(ctx, req.Sid)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("GetSceneByID failed: %v", err)
		return nil, ErrSceneNotFound
	}
	if existingScene == nil {
		return nil, ErrSceneNotFound
	}

	// 更新场景
	scene := &Scene{
		SID:      req.Sid,
		Name:     req.Name,
		Describe: req.Describe,
		View:     req.View,
		Location: req.Location,
		Article:  req.Article,
	}

	updatedScene, err := uc.repo.UpdateScene(ctx, scene)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("UpdateScene failed: %v", err)
		return nil, ErrUpdateSceneFailed
	}

	return &v1.SceneMessage{
		Sid:       updatedScene.SID,
		Name:      updatedScene.Name,
		Describe:  updatedScene.Describe,
		View:      updatedScene.View,
		Location:  updatedScene.Location,
		Article:   updatedScene.Article,
		CreatedAt: updatedScene.CreatedAt.Format(time.RFC3339),
		UpdatedAt: updatedScene.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// SearchScene 搜索场景
func (uc *SceneUsecase) SearchScene(ctx context.Context, keyword string) ([]*v1.SceneMessage, error) {
	uc.log.WithContext(ctx).Infof("SearchScene: keyword=%s", keyword)

	scenes, err := uc.repo.SearchScene(ctx, keyword)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("SearchScene failed: %v", err)
		return nil, ErrSearchSceneFailed
	}

	result := make([]*v1.SceneMessage, 0, len(scenes))
	for _, s := range scenes {
		result = append(result, &v1.SceneMessage{
			Sid:       s.SID,
			Name:      s.Name,
			Describe:  s.Describe,
			View:      s.View,
			Location:  s.Location,
			Article:   s.Article,
			CreatedAt: s.CreatedAt.Format(time.RFC3339),
			UpdatedAt: s.UpdatedAt.Format(time.RFC3339),
		})
	}

	return result, nil
}

// ListScenes 获取场景列表
func (uc *SceneUsecase) ListScenes(ctx context.Context, limit int) ([]*v1.SceneMessage, error) {
	uc.log.WithContext(ctx).Infof("ListScenes: limit=%d", limit)

	scenes, err := uc.repo.ListScenes(ctx, limit)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("ListScenes failed: %v", err)
		return nil, ErrDatabaseError
	}

	result := make([]*v1.SceneMessage, 0, len(scenes))
	for _, s := range scenes {
		result = append(result, &v1.SceneMessage{
			Sid:       s.SID,
			Name:      s.Name,
			Describe:  s.Describe,
			View:      s.View,
			Location:  s.Location,
			Article:   s.Article,
			CreatedAt: s.CreatedAt.Format(time.RFC3339),
			UpdatedAt: s.UpdatedAt.Format(time.RFC3339),
		})
	}

	return result, nil
}

// GetSceneByID 根据ID获取场景
func (uc *SceneUsecase) GetSceneByID(ctx context.Context, sid string) (*v1.SceneMessage, error) {
	uc.log.WithContext(ctx).Infof("GetSceneByID: SID=%s", sid)

	scene, err := uc.repo.GetSceneByID(ctx, sid)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("GetSceneByID failed: %v", err)
		return nil, ErrDatabaseError
	}

	if scene == nil {
		return nil, ErrSceneNotFound
	}

	return &v1.SceneMessage{
		Sid:       scene.SID,
		Name:      scene.Name,
		Describe:  scene.Describe,
		View:      scene.View,
		Location:  scene.Location,
		Article:   scene.Article,
		CreatedAt: scene.CreatedAt.Format(time.RFC3339),
		UpdatedAt: scene.UpdatedAt.Format(time.RFC3339),
	}, nil
}
