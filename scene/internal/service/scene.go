package service

import (
	"context"

	pb "github.com/knoci/roaming-world/scene/api/scene/v1"
)

type SceneService struct {
	pb.UnimplementedSceneServer
}

func NewSceneService() *SceneService {
	return &SceneService{}
}

func (s *SceneService) CreateScene(ctx context.Context, req *pb.CreateSceneRequest) (*pb.CreateSceneReply, error) {
	return &pb.CreateSceneReply{}, nil
}
func (s *SceneService) DeleteScene(ctx context.Context, req *pb.DeleteSceneRequest) (*pb.DeleteSceneReply, error) {
	return &pb.DeleteSceneReply{}, nil
}
func (s *SceneService) UpdateScene(ctx context.Context, req *pb.UpdateSceneRequest) (*pb.UpdateSceneReply, error) {
	return &pb.UpdateSceneReply{}, nil
}
func (s *SceneService) SearchScene(ctx context.Context, req *pb.SearchSceneRequest) (*pb.SearchSceneReply, error) {
	return &pb.SearchSceneReply{}, nil
}
func (s *SceneService) ListScenes(ctx context.Context, req *pb.ListScenesRequest) (*pb.ListScenesReply, error) {
	return &pb.ListScenesReply{}, nil
}
func (s *SceneService) GetSceneByID(ctx context.Context, req *pb.GetSceneByIDRequest) (*pb.GetSceneByIDReply, error) {
	return &pb.GetSceneByIDReply{}, nil
}
