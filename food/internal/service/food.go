package service

import (
	"context"
	"time"

	pb "github.com/knoci/roaming-world/food/api/food/v1"
	"github.com/knoci/roaming-world/food/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"
)

func NewFoodService(food *biz.FoodUsecase, logger log.Logger) *FoodService {
	return &FoodService{
		food: food,
		log:  log.NewHelper(logger),
	}
}

func (s *FoodService) CreateFood(ctx context.Context, req *pb.CreateFoodRequest) (*pb.CreateFoodReply, error) {
	s.log.WithContext(ctx).Infof("CreateFood: %v", req.Name)

	httpReq, _ := http.RequestFromServerContext(ctx)
	authHeader := httpReq.Header.Get("Authorization")
	if len(authHeader) == 0 || authHeader != "knoci1337" {
		return nil, biz.ErrUnauthorized
	}

	food, err := s.food.CreateFood(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("CreateFood error: %v", err)
		return nil, err
	}

	// 转换为API响应格式
	return &pb.CreateFoodReply{
		Food: &pb.FoodMessage{
			Fid:       food.FID,
			View:      food.View,
			Name:      food.Name,
			Describe:  food.Describe,
			Recipe:    food.Recipe,
			Article:   food.Article,
			Location:  food.Location,
			CreatedAt: food.CreatedAt.Format(time.RFC3339),
			UpdatedAt: food.UpdatedAt.Format(time.RFC3339),
		},
	}, nil
}
func (s *FoodService) GetFoodList(ctx context.Context, req *pb.GetFoodListRequest) (*pb.GetFoodListReply, error) {
	s.log.WithContext(ctx).Info("GetFoodList")

	foods, err := s.food.GetFoodList(ctx)
	if err != nil {
		s.log.WithContext(ctx).Errorf("GetFoodList error: %v", err)
		return nil, err
	}

	reply := &pb.GetFoodListReply{}
	for _, food := range foods {
		reply.Foods = append(reply.Foods, &pb.FoodMessage{
			Fid:       food.FID,
			View:      food.View,
			Name:      food.Name,
			Describe:  food.Describe,
			Recipe:    food.Recipe,
			Article:   food.Article,
			Location:  food.Location,
			CreatedAt: food.CreatedAt.Format(time.RFC3339),
			UpdatedAt: food.UpdatedAt.Format(time.RFC3339),
		})
	}

	return reply, nil
}
func (s *FoodService) GetRandomFood(ctx context.Context, req *pb.GetRandomFoodRequest) (*pb.GetRandomFoodReply, error) {
	s.log.WithContext(ctx).Info("GetRandomFood")

	food, err := s.food.GetRandomFood(ctx)
	if err != nil {
		s.log.WithContext(ctx).Errorf("GetRandomFood error: %v", err)
		return nil, err
	}

	if food == nil {
		return &pb.GetRandomFoodReply{}, nil
	}

	return &pb.GetRandomFoodReply{
		Food: &pb.FoodMessage{
			Fid:       food.FID,
			View:      food.View,
			Name:      food.Name,
			Describe:  food.Describe,
			Recipe:    food.Recipe,
			Article:   food.Article,
			Location:  food.Location,
			CreatedAt: food.CreatedAt.Format(time.RFC3339),
			UpdatedAt: food.UpdatedAt.Format(time.RFC3339),
		},
	}, nil
}
