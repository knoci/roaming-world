package service

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	pb "github.com/knoci/roaming-world/audiobook/api/audiobook/v1"
	"github.com/knoci/roaming-world/audiobook/internal/biz"
)

func NewAudiobookService(audiobook *biz.AudiobookUsecase, logger log.Logger) *AudiobookService {
	return &AudiobookService{
		audiobook: audiobook,
		log:       log.NewHelper(logger),
	}
}

func (s *AudiobookService) CreateAudiobook(ctx context.Context, req *pb.CreateAudiobookRequest) (*pb.CreateAudiobookReply, error) {
	s.log.WithContext(ctx).Infof("audiobookService: CreateAudiobook received: Name=%s, Author=%s", req.Name, req.Author)

	if !isAdmin(ctx) {
		return nil, biz.ErrUnauthorized
	}

	audiobook, err := s.audiobook.CreateAudiobook(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("audiobookService: CreateAudiobook failed: %v", err)
		return nil, err
	}
	return &pb.CreateAudiobookReply{Audiobook: audiobook}, nil
}

func (s *AudiobookService) CreateAudiobookDetail(ctx context.Context, req *pb.CreateAudiobookDetailRequest) (*pb.CreateAudiobookDetailReply, error) {
	s.log.WithContext(ctx).Infof("audiobookService: CreateAudiobookDetail received: BID=%s, Chapter=%d", req.Bid, req.Chapter)

	if !isAdmin(ctx) {
		return nil, biz.ErrUnauthorized
	}

	detail, err := s.audiobook.CreateAudiobookDetail(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("audiobookService: CreateAudiobookDetail failed: %v", err)
		return nil, err
	}
	return &pb.CreateAudiobookDetailReply{Detail: detail}, nil
}

func (s *AudiobookService) GetAudiobooks(ctx context.Context, req *pb.GetAudiobooksRequest) (*pb.GetAudiobooksReply, error) {
	s.log.WithContext(ctx).Info("audiobookService: GetAudiobooks received")

	audiobooks, err := s.audiobook.GetAudiobooks(ctx)
	if err != nil {
		s.log.WithContext(ctx).Errorf("audiobookService: GetAudiobooks failed: %v", err)
		return nil, err
	}
	return &pb.GetAudiobooksReply{Audiobooks: audiobooks}, nil
}

func (s *AudiobookService) GetAudiobookDetails(ctx context.Context, req *pb.GetAudiobookDetailsRequest) (*pb.GetAudiobookDetailsReply, error) {
	s.log.WithContext(ctx).Infof("audiobookService: GetAudiobookDetails received: BID=%s", req.Bid)

	details, err := s.audiobook.GetAudiobookDetails(ctx, req.Bid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("audiobookService: GetAudiobookDetails failed: %v", err)
		return nil, err
	}
	return &pb.GetAudiobookDetailsReply{Details: details}, nil
}
