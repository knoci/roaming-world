package service

import (
	"context"

	v1 "github.com/knoci/roaming-world/audiobook/api/audiobook/v1"
	"github.com/knoci/roaming-world/audiobook/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
)



// CreateAudiobook 创建有声书
func (s *AudiobookService) CreateAudiobook(ctx context.Context, req *v1.CreateAudiobookRequest) (*v1.CreateAudiobookReply, error) {
	s.log.WithContext(ctx).Infof("CreateAudiobook received: Name=%s, Author=%s", req.Name, req.Author)

	audiobook, err := s.uc.CreateAudiobook(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("CreateAudiobook failed: %v", err)
		return nil, err
	}

	return &v1.CreateAudiobookReply{
		Audiobook: audiobook,
	}, nil
}

// CreateAudiobookDetail 创建有声书章节
func (s *AudiobookService) CreateAudiobookDetail(ctx context.Context, req *v1.CreateAudiobookDetailRequest) (*v1.CreateAudiobookDetailReply, error) {
	s.log.WithContext(ctx).Infof("CreateAudiobookDetail received: BID=%s, Chapter=%d", req.Bid, req.Chapter)

	detail, err := s.uc.CreateAudiobookDetail(ctx, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("CreateAudiobookDetail failed: %v", err)
		return nil, err
	}

	return &v1.CreateAudiobookDetailReply{
		Detail: detail,
	}, nil
}

// GetAudiobooks 获取所有有声书
func (s *AudiobookService) GetAudiobooks(ctx context.Context, req *v1.GetAudiobooksRequest) (*v1.GetAudiobooksReply, error) {
	s.log.WithContext(ctx).Info("GetAudiobooks received")

	audiobooks, err := s.uc.GetAudiobooks(ctx)
	if err != nil {
		s.log.WithContext(ctx).Errorf("GetAudiobooks failed: %v", err)
		return nil, err
	}

	return &v1.GetAudiobooksReply{
		Audiobooks: audiobooks,
	}, nil
}

// GetAudiobookDetails 获取指定有声书的所有章节
func (s *AudiobookService) GetAudiobookDetails(ctx context.Context, req *v1.GetAudiobookDetailsRequest) (*v1.GetAudiobookDetailsReply, error) {
	s.log.WithContext(ctx).Infof("GetAudiobookDetails received: BID=%s", req.Bid)

	details, err := s.uc.GetAudiobookDetails(ctx, req.Bid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("GetAudiobookDetails failed: %v", err)
		return nil, err
	}

	return &v1.GetAudiobookDetailsReply{
		Details: details,
	}, nil
}
