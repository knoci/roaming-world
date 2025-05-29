package service

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"
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
	s.log.WithContext(ctx).Infof("audiobbokService: CreateAudiobook received: Name=%s, Author=%s", req.Name, req.Author)

	httpReq, _ := http.RequestFromServerContext(ctx)
	authHeader := httpReq.Header.Get("Authorization")
	if len(authHeader) == 0 || authHeader != "knoci1337" {
		return nil, biz.ErrUnauthorized
	}

	audiobook, err := s.audiobook.CreateAudiobook(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("audiobbokService: CreateAudiobook failed: %v", err)
		return nil, err
	}

	return &pb.CreateAudiobookReply{
		Audiobook: audiobook,
	}, nil
}
func (s *AudiobookService) CreateAudiobookDetail(ctx context.Context, req *pb.CreateAudiobookDetailRequest) (*pb.CreateAudiobookDetailReply, error) {
	s.log.WithContext(ctx).Infof("audiobbokService: CreateAudiobookDetail received: BID=%s, Chapter=%d", req.Bid, req.Chapter)

	detail, err := s.audiobook.CreateAudiobookDetail(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("audiobbokService: CreateAudiobookDetail failed: %v", err)
		return nil, err
	}

	return &pb.CreateAudiobookDetailReply{
		Detail: detail,
	}, nil
}
func (s *AudiobookService) GetAudiobooks(ctx context.Context, req *pb.GetAudiobooksRequest) (*pb.GetAudiobooksReply, error) {
	s.log.WithContext(ctx).Info("audiobbokService: GetAudiobooks received")

	audiobooks, err := s.audiobook.GetAudiobooks(ctx)
	if err != nil {
		s.log.WithContext(ctx).Errorf("audiobbokService: GetAudiobooks failed: %v", err)
		return nil, err
	}

	return &pb.GetAudiobooksReply{
		Audiobooks: audiobooks,
	}, nil
}
func (s *AudiobookService) GetAudiobookDetails(ctx context.Context, req *pb.GetAudiobookDetailsRequest) (*pb.GetAudiobookDetailsReply, error) {
	s.log.WithContext(ctx).Infof("audiobbokService: GetAudiobookDetails received: BID=%s", req.Bid)

	details, err := s.audiobook.GetAudiobookDetails(ctx, req.Bid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("audiobbokService: GetAudiobookDetails failed: %v", err)
		return nil, err
	}

	return &pb.GetAudiobookDetailsReply{
		Details: details,
	}, nil
}