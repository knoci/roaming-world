package service

import (
	"context"

	pb "github.com/knoci/roaming-world/food/api/food/v1"
)

type FoodService struct {
	pb.UnimplementedFoodServer
}

func NewFoodService() *FoodService {
	return &FoodService{}
}

func (s *FoodService) CreateFood(ctx context.Context, req *pb.CreateFoodRequest) (*pb.CreateFoodReply, error) {
	return &pb.CreateFoodReply{}, nil
}
func (s *FoodService) GetFoodList(ctx context.Context, req *pb.GetFoodListRequest) (*pb.GetFoodListReply, error) {
	return &pb.GetFoodListReply{}, nil
}
func (s *FoodService) GetRandomFood(ctx context.Context, req *pb.GetRandomFoodRequest) (*pb.GetRandomFoodReply, error) {
	return &pb.GetRandomFoodReply{}, nil
}
