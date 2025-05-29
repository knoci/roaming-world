package biz

import (
	"context"
	"time"

	v1 "github.com/knoci/roaming-world/audiobook/api/audiobook/v1"

	"github.com/go-kratos/kratos/v2/log"
)

// Audiobook 有声书实体
type Audiobook struct {
	BID         string
	View        string
	Author      string
	Name        string
	Playcount   int
	Chapternum  int
	Rating      float64
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AudiobookDetail 有声书章节实体
type AudiobookDetail struct {
	DID       string
	BID       string
	Chapter   int
	Audio     string
	Name      string
	Duration  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AudiobookRepo 有声书仓库接口
type AudiobookRepo interface {
	CreateAudiobook(ctx context.Context, audiobook *Audiobook) (*Audiobook, error)
	CreateAudiobookDetail(ctx context.Context, detail *AudiobookDetail) (*AudiobookDetail, error)
	GetAudiobooks(ctx context.Context) ([]*Audiobook, error)
	GetAudiobookDetails(ctx context.Context, bid string) ([]*AudiobookDetail, error)
}

// AudiobookUsecase 有声书用例
type AudiobookUsecase struct {
	repo AudiobookRepo
	log  *log.Helper
}

// NewAudiobookUsecase 创建有声书用例
func NewAudiobookUsecase(repo AudiobookRepo, logger log.Logger) *AudiobookUsecase {
	return &AudiobookUsecase{repo: repo, log: log.NewHelper(logger)}
}

// CreateAudiobook 创建有声书
func (uc *AudiobookUsecase) CreateAudiobook(ctx context.Context, req *v1.CreateAudiobookRequest) (*v1.AudiobookMessage, error) {
	uc.log.WithContext(ctx).Infof("audiobookUsecase: CreateAudiobook: %v", req.Name)

	audiobook := &Audiobook{
		View:        req.View,
		Author:      req.Author,
		Name:        req.Name,
		Rating:      req.Rating,
		Description: req.Description,
		Playcount:   0,
		Chapternum:  0,
	}

	createdAudiobook, err := uc.repo.CreateAudiobook(ctx, audiobook)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("audiobookUsecase: CreateAudiobook failed: %v", err)
		return nil, ErrCreateAudiobookFailed
	}

	return &v1.AudiobookMessage{
		Bid:         createdAudiobook.BID,
		View:        createdAudiobook.View,
		Author:      createdAudiobook.Author,
		Name:        createdAudiobook.Name,
		Playcount:   int32(createdAudiobook.Playcount),
		Chapternum:  int32(createdAudiobook.Chapternum),
		Rating:      createdAudiobook.Rating,
		Description: createdAudiobook.Description,
		CreatedAt:   createdAudiobook.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   createdAudiobook.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// CreateAudiobookDetail 创建有声书章节
func (uc *AudiobookUsecase) CreateAudiobookDetail(ctx context.Context, req *v1.CreateAudiobookDetailRequest) (*v1.AudiobookDetailMessage, error) {
	uc.log.WithContext(ctx).Infof("audiobookUsecase: CreateAudiobookDetail BID=%s, Chapter=%d", req.Bid, req.Chapter)

	detail := &AudiobookDetail{
		BID:      req.Bid,
		Chapter:  int(req.Chapter),
		Audio:    req.Audio,
		Name:     req.Name,
		Duration: int(req.Duration),
	}

	createdDetail, err := uc.repo.CreateAudiobookDetail(ctx, detail)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("audiobookUsecase: CreateAudiobookDetail failed: %v", err)
		return nil, ErrCreateDetailFailed
	}

	return &v1.AudiobookDetailMessage{
		Did:       createdDetail.DID,
		Bid:       createdDetail.BID,
		Chapter:   int32(createdDetail.Chapter),
		Audio:     createdDetail.Audio,
		Name:      createdDetail.Name,
		Duration:  int32(createdDetail.Duration),
		CreatedAt: createdDetail.CreatedAt.Format(time.RFC3339),
		UpdatedAt: createdDetail.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// GetAudiobooks 获取所有有声书
func (uc *AudiobookUsecase) GetAudiobooks(ctx context.Context) ([]*v1.AudiobookMessage, error) {
	uc.log.WithContext(ctx).Info("audiobookUsecase: GetAudiobooks")

	audiobooks, err := uc.repo.GetAudiobooks(ctx)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("audiobookUsecase: GetAudiobooks failed: %v", err)
		return nil, ErrDatabaseError
	}

	result := make([]*v1.AudiobookMessage, 0, len(audiobooks))
	for _, a := range audiobooks {
		result = append(result, &v1.AudiobookMessage{
			Bid:         a.BID,
			View:        a.View,
			Author:      a.Author,
			Name:        a.Name,
			Playcount:   int32(a.Playcount),
			Chapternum:  int32(a.Chapternum),
			Rating:      a.Rating,
			Description: a.Description,
			CreatedAt:   a.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   a.UpdatedAt.Format(time.RFC3339),
		})
	}

	return result, nil
}

// GetAudiobookDetails 获取指定有声书的所有章节
func (uc *AudiobookUsecase) GetAudiobookDetails(ctx context.Context, bid string) ([]*v1.AudiobookDetailMessage, error) {
	uc.log.WithContext(ctx).Infof("audiobookUsecase: GetAudiobookDetails: BID=%s", bid)

	details, err := uc.repo.GetAudiobookDetails(ctx, bid)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("audiobookUsecase: GetAudiobookDetails failed: %v", err)
		return nil, ErrDetailNotFound
	}

	result := make([]*v1.AudiobookDetailMessage, 0, len(details))
	for _, d := range details {
		result = append(result, &v1.AudiobookDetailMessage{
			Did:       d.DID,
			Bid:       d.BID,
			Chapter:   int32(d.Chapter),
			Audio:     d.Audio,
			Name:      d.Name,
			Duration:  int32(d.Duration),
			CreatedAt: d.CreatedAt.Format(time.RFC3339),
			UpdatedAt: d.UpdatedAt.Format(time.RFC3339),
		})
	}

	return result, nil
}
