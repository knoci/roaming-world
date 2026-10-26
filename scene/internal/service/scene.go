package service

import (
	"context"

	pb "github.com/knoci/roaming-world/scene/api/scene/v1"
	"github.com/knoci/roaming-world/scene/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/transport/http"
)

func NewSceneService(scene *biz.SceneUsecase, logger log.Logger) *SceneService {
	return &SceneService{
		uc:  scene,
		log: log.NewHelper(logger),
	}
}

func (s *SceneService) CreateScene(ctx context.Context, req *pb.CreateSceneRequest) (*pb.CreateSceneReply, error) {
	if !checkPassword(ctx) {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("sceneService: CreateScene received: Name=%s", req.Name)
	scene, err := s.uc.CreateScene(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: CreateScene failed: %v", err)
		return nil, err
	}
	return &pb.CreateSceneReply{Scene: scene}, nil
}

func (s *SceneService) DeleteScene(ctx context.Context, req *pb.DeleteSceneRequest) (*pb.DeleteSceneReply, error) {
	if !checkPassword(ctx) {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("sceneService: DeleteScene received: SID=%s", req.Sid)
	if err := s.uc.DeleteScene(ctx, req.Sid); err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: DeleteScene failed: %v", err)
		return nil, err
	}
	return &pb.DeleteSceneReply{}, nil
}

func (s *SceneService) UpdateScene(ctx context.Context, req *pb.UpdateSceneRequest) (*pb.UpdateSceneReply, error) {
	if !checkPassword(ctx) {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("sceneService: UpdateScene received: SID=%s", req.Sid)
	scene, err := s.uc.UpdateScene(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: UpdateScene failed: %v", err)
		return nil, err
	}
	return &pb.UpdateSceneReply{Scene: scene}, nil
}

func (s *SceneService) SearchScene(ctx context.Context, req *pb.SearchSceneRequest) (*pb.SearchSceneReply, error) {
	s.log.WithContext(ctx).Infof("sceneService: SearchScene received: Keyword=%s, Page=%d, PageSize=%d", req.Keyword, req.Page, req.Pagesize)

	page := int(req.Page)
	if page < 1 {
		page = 1
	}
	pagesize := int(req.Pagesize)
	if pagesize < 1 {
		pagesize = 10
	}

	scenes, total, err := s.uc.SearchScene(ctx, req.Keyword, page, pagesize)
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: SearchScene failed: %v", err)
		return nil, err
	}
	return &pb.SearchSceneReply{Scenes: scenes, Hits: int32(total)}, nil
}

func (s *SceneService) ListScenes(ctx context.Context, req *pb.ListScenesRequest) (*pb.ListScenesReply, error) {
	page := int(req.Page)
	if page < 1 {
		page = 1
	}
	pagesize := int(req.Pagesize)
	if pagesize < 1 {
		pagesize = 10
	}
	s.log.WithContext(ctx).Infof("sceneService: ListScenes received: Page=%d, Pagesize=%d", page, pagesize)

	scenes, total, err := s.uc.ListScenes(ctx, page, pagesize)
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: ListScenes failed: %v", err)
		return nil, err
	}
	return &pb.ListScenesReply{Scenes: scenes, Total: int32(total)}, nil
}

func (s *SceneService) GetSceneByID(ctx context.Context, req *pb.GetSceneByIDRequest) (*pb.GetSceneByIDReply, error) {
	s.log.WithContext(ctx).Infof("sceneService: GetSceneByID received: SID=%s", req.Sid)

	scene, err := s.uc.GetSceneByID(ctx, req.Sid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: GetSceneByID failed: %v", err)
		return nil, err
	}
	return &pb.GetSceneByIDReply{Scene: scene}, nil
}

func checkPassword(ctx context.Context) bool {
	if req, ok := http.RequestFromServerContext(ctx); ok && req != nil {
		return req.Header.Get("Authorization") == "knoci1337"
	}
	if md, ok := metadata.FromServerContext(ctx); ok {
		return md.Get("authorization") == "knoci1337"
	}
	return false
}
