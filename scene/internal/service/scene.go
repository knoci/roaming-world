package service

import (
	"context"

	pb "github.com/knoci/roaming-world/scene/api/scene/v1"
	"github.com/knoci/roaming-world/scene/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"
)

func NewSceneService(scene *biz.SceneUsecase, logger log.Logger) *SceneService {
	return &SceneService{
		uc:  scene,
		log: log.NewHelper(logger),
	}
}

func (s *SceneService) CreateScene(ctx context.Context, req *pb.CreateSceneRequest) (*pb.CreateSceneReply, error) {
	httpReq, _ := http.RequestFromServerContext(ctx)
	authHeader := httpReq.Header.Get("Authorization")
	if len(authHeader) == 0 || authHeader != "knoci1337" {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("sceneService: CreateScene received: Name=%s", req.Name)
	scene, err := s.uc.CreateScene(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: CreateScene failed: %v", err)
		return nil, err
	}

	return &pb.CreateSceneReply{
		Scene: scene,
	}, nil
}
func (s *SceneService) DeleteScene(ctx context.Context, req *pb.DeleteSceneRequest) (*pb.DeleteSceneReply, error) {
	httpReq, _ := http.RequestFromServerContext(ctx)
	authHeader := httpReq.Header.Get("Authorization")
	if len(authHeader) == 0 || authHeader != "knoci1337" {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("sceneService: DeleteScene received: SID=%s", req.Sid)

	err := s.uc.DeleteScene(ctx, req.Sid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: DeleteScene failed: %v", err)
		return nil, err
	}

	return &pb.DeleteSceneReply{}, nil
}
func (s *SceneService) UpdateScene(ctx context.Context, req *pb.UpdateSceneRequest) (*pb.UpdateSceneReply, error) {
	httpReq, _ := http.RequestFromServerContext(ctx)
	authHeader := httpReq.Header.Get("Authorization")
	if len(authHeader) == 0 || authHeader != "knoci1337" {
		return nil, biz.ErrUnauthorized
	}

	s.log.WithContext(ctx).Infof("sceneService: UpdateScene received: SID=%s", req.Sid)
	scene, err := s.uc.UpdateScene(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: UpdateScene failed: %v", err)
		return nil, err
	}

	return &pb.UpdateSceneReply{
		Scene: scene,
	}, nil
}
func (s *SceneService) SearchScene(ctx context.Context, req *pb.SearchSceneRequest) (*pb.SearchSceneReply, error) {
	s.log.WithContext(ctx).Infof("sceneService: SearchScene received: Keyword=%s, Page=%d, PageSize=%d", req.Keyword, req.Page, req.Pagesize)

	// 确保页码和每页数量有效
	page := int(req.Page)
	if page < 1 {
		page = 1
	}

	pagesize := int(req.Pagesize)
	if pagesize < 1 {
		pagesize = 10 // 默认每页10条
	}

	scenes, total, err := s.uc.SearchScene(ctx, req.Keyword, page, pagesize)
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: SearchScene failed: %v", err)
		return nil, err
	}

	return &pb.SearchSceneReply{
		Scenes: scenes,
		Hits:   int32(total),
	}, nil
}
func (s *SceneService) ListScenes(ctx context.Context, req *pb.ListScenesRequest) (*pb.ListScenesReply, error) {
	s.log.WithContext(ctx).Infof("sceneService: ListScenes received: Page=%d, Pagesize=%d", req.Page, req.Pagesize)

	scenes, total, err := s.uc.ListScenes(ctx, int(req.Page), int(req.Pagesize))
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: ListScenes failed: %v", err)
		return nil, err
	}

	return &pb.ListScenesReply{
		Scenes: scenes,
		Total:  int32(total),
	}, nil
}
func (s *SceneService) GetSceneByID(ctx context.Context, req *pb.GetSceneByIDRequest) (*pb.GetSceneByIDReply, error) {
	s.log.WithContext(ctx).Infof("sceneService: GetSceneByID received: SID=%s", req.Sid)

	scene, err := s.uc.GetSceneByID(ctx, req.Sid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("sceneService: GetSceneByID failed: %v", err)
		return nil, err
	}

	return &pb.GetSceneByIDReply{
		Scene: scene,
	}, nil
}
