package service

import (
	"context"

	v1 "scene/api/scene/v1"
	"scene/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
)

// SceneService 场景服务实现


// NewSceneService 创建场景服务实例
func NewSceneService(uc *biz.SceneUsecase, logger log.Logger) *SceneService {
	return &SceneService{
		uc:  uc,
		log: log.NewHelper(logger),
	}
}

// CreateScene 创建场景
func (s *SceneService) CreateScene(ctx context.Context, req *v1.CreateSceneRequest) (*v1.CreateSceneReply, error) {
	s.log.WithContext(ctx).Infof("CreateScene received: Name=%s", req.Name)

	scene, err := s.uc.CreateScene(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("CreateScene failed: %v", err)
		return nil, err
	}

	return &v1.CreateSceneReply{
		Scene: scene,
	}, nil
}

// DeleteScene 删除场景
func (s *SceneService) DeleteScene(ctx context.Context, req *v1.DeleteSceneRequest) (*v1.DeleteSceneReply, error) {
	s.log.WithContext(ctx).Infof("DeleteScene received: SID=%s", req.Sid)

	err := s.uc.DeleteScene(ctx, req.Sid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("DeleteScene failed: %v", err)
		return nil, err
	}

	return &v1.DeleteSceneReply{}, nil
}

// UpdateScene 更新场景
func (s *SceneService) UpdateScene(ctx context.Context, req *v1.UpdateSceneRequest) (*v1.UpdateSceneReply, error) {
	s.log.WithContext(ctx).Infof("UpdateScene received: SID=%s", req.Sid)

	scene, err := s.uc.UpdateScene(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("UpdateScene failed: %v", err)
		return nil, err
	}

	return &v1.UpdateSceneReply{
		Scene: scene,
	}, nil
}

// SearchScene 搜索场景
func (s *SceneService) SearchScene(ctx context.Context, req *v1.SearchSceneRequest) (*v1.SearchSceneReply, error) {
	s.log.WithContext(ctx).Infof("SearchScene received: Keyword=%s", req.Keyword)

	scenes, err := s.uc.SearchScene(ctx, req.Keyword)
	if err != nil {
		s.log.WithContext(ctx).Errorf("SearchScene failed: %v", err)
		return nil, err
	}

	return &v1.SearchSceneReply{
		Scenes: scenes,
	}, nil
}

// ListScenes 获取场景列表
func (s *SceneService) ListScenes(ctx context.Context, req *v1.ListScenesRequest) (*v1.ListScenesReply, error) {
	s.log.WithContext(ctx).Infof("ListScenes received: Limit=%d", req.Limit)

	scenes, err := s.uc.ListScenes(ctx, int(req.Limit))
	if err != nil {
		s.log.WithContext(ctx).Errorf("ListScenes failed: %v", err)
		return nil, err
	}

	return &v1.ListScenesReply{
		Scenes: scenes,
	}, nil
}

// GetSceneByID 根据ID获取场景
func (s *SceneService) GetSceneByID(ctx context.Context, req *v1.GetSceneByIDRequest) (*v1.GetSceneByIDReply, error) {
	s.log.WithContext(ctx).Infof("GetSceneByID received: SID=%s", req.Sid)

	scene, err := s.uc.GetSceneByID(ctx, req.Sid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("GetSceneByID failed: %v", err)
		return nil, err
	}

	return &v1.GetSceneByIDReply{
		Scene: scene,
	}, nil
}
