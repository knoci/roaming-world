package service

import (
	"context"

	pb "audiobook/api/audiobook/v1"
)

func NewAudiobookService() *AudiobookService {
	return &AudiobookService{}
}

func (s *AudiobookService) CreateAudiobook(ctx context.Context, req *pb.CreateAudiobookRequest) (*pb.CreateAudiobookReply, error) {
	return &pb.CreateAudiobookReply{}, nil
}
func (s *AudiobookService) CreateAudiobookDetail(ctx context.Context, req *pb.CreateAudiobookDetailRequest) (*pb.CreateAudiobookDetailReply, error) {
	return &pb.CreateAudiobookDetailReply{}, nil
}
func (s *AudiobookService) GetAudiobooks(ctx context.Context, req *pb.GetAudiobooksRequest) (*pb.GetAudiobooksReply, error) {
	return &pb.GetAudiobooksReply{}, nil
}
func (s *AudiobookService) GetAudiobookDetails(ctx context.Context, req *pb.GetAudiobookDetailsRequest) (*pb.GetAudiobookDetailsReply, error) {
	return &pb.GetAudiobookDetailsReply{}, nil
}
