package biz

import (
	"context"
	"time"

	v1 "github.com/knoci/roaming-world/article/api/article/v1"

	"github.com/go-kratos/kratos/v2/log"
)

// Article 文章实体。文章表保留作者名和头像快照，避免列表页逐条调用用户服务。
type Article struct {
	AID       string
	UID       string
	Name      string
	Avatar    string
	Title     string
	View      []string
	Content   string
	Likes     int
	Comments  int
	Favorite  int
	Video     bool
	Videoid   int
	Height    int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ArticleRepo 文章仓库接口。
type ArticleRepo interface {
	Create(ctx context.Context, article *Article) (*Article, error)
	Update(ctx context.Context, article *Article) (*Article, error)
	Delete(ctx context.Context, aid string) error
	FindByID(ctx context.Context, aid string) (*Article, error)
	List(ctx context.Context, page, limit int, way string, reverse bool) ([]*Article, int64, error)
	Like(ctx context.Context, uid, aid string) (bool, int, error)
	Favorite(ctx context.Context, uid, aid string) (bool, int, error)
	GetUserLikeList(ctx context.Context, uid string) ([]string, error)
	GetUserFavoriteList(ctx context.Context, uid string) ([]string, error)
	GetVideoArticle(ctx context.Context, videoid int) (*Article, error)
	GetMaxVideoID(ctx context.Context) (int, error)
}

// ArticleUsecase 文章用例。
type ArticleUsecase struct {
	repo ArticleRepo
	log  *log.Helper
}

func NewArticleUsecase(repo ArticleRepo, logger log.Logger) *ArticleUsecase {
	return &ArticleUsecase{repo: repo, log: log.NewHelper(logger)}
}

func (uc *ArticleUsecase) Create(ctx context.Context, uid, name, avatar string, req *v1.CreateArticleRequest) (*Article, error) {
	article := &Article{
		UID:     uid,
		Name:    name,
		Avatar:  avatar,
		Title:   req.Title,
		View:    req.View,
		Content: req.Content,
		Video:   req.Video,
		Height:  int(req.Height),
	}
	created, err := uc.repo.Create(ctx, article)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("articleUsecase: create article failed: %v", err)
		return nil, ErrCreateArticleFailed
	}
	return created, nil
}

func (uc *ArticleUsecase) Update(ctx context.Context, uid, avatar string, req *v1.UpdateArticleRequest) (*Article, error) {
	article, err := uc.repo.FindByID(ctx, req.Aid)
	if err != nil {
		return nil, ErrArticleNotFound
	}
	if article.UID != uid {
		return nil, ErrNoPermission
	}

	article.Avatar = avatar
	article.Title = req.Title
	article.View = req.View
	article.Content = req.Content
	article.Video = req.Video
	article.Height = int(req.Height)

	updated, err := uc.repo.Update(ctx, article)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("articleUsecase: update article failed: %v", err)
		return nil, ErrUpdateArticleFailed
	}
	return updated, nil
}

func (uc *ArticleUsecase) Delete(ctx context.Context, uid, aid string) error {
	article, err := uc.repo.FindByID(ctx, aid)
	if err != nil {
		return ErrArticleNotFound
	}
	if article.UID != uid {
		return ErrNoPermission
	}
	if err := uc.repo.Delete(ctx, aid); err != nil {
		uc.log.WithContext(ctx).Errorf("articleUsecase: delete article failed: %v", err)
		return ErrDeleteArticleFailed
	}
	return nil
}

func (uc *ArticleUsecase) Like(ctx context.Context, uid, aid string) (bool, int, error) {
	if _, err := uc.repo.FindByID(ctx, aid); err != nil {
		return false, 0, ErrArticleNotFound
	}
	return uc.repo.Like(ctx, uid, aid)
}

func (uc *ArticleUsecase) Favorite(ctx context.Context, uid, aid string) (bool, int, error) {
	if _, err := uc.repo.FindByID(ctx, aid); err != nil {
		return false, 0, ErrArticleNotFound
	}
	return uc.repo.Favorite(ctx, uid, aid)
}

func (uc *ArticleUsecase) List(ctx context.Context, page, limit int, way string, reverse bool) ([]*Article, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 0 {
		limit = 0
	}
	return uc.repo.List(ctx, page, limit, way, reverse)
}

func (uc *ArticleUsecase) FindByID(ctx context.Context, aid string) (*Article, error) {
	article, err := uc.repo.FindByID(ctx, aid)
	if err != nil {
		return nil, ErrArticleNotFound
	}
	return article, nil
}

func (uc *ArticleUsecase) GetVideoArticle(ctx context.Context, videoid int) (*Article, error) {
	article, err := uc.repo.GetVideoArticle(ctx, videoid)
	if err != nil {
		return nil, ErrArticleNotFound
	}
	return article, nil
}

func (uc *ArticleUsecase) GetMaxVideoID(ctx context.Context) (int, error) {
	return uc.repo.GetMaxVideoID(ctx)
}

func (uc *ArticleUsecase) GetUserLikeList(ctx context.Context, uid string) ([]string, error) {
	return uc.repo.GetUserLikeList(ctx, uid)
}

func (uc *ArticleUsecase) GetUserFavoriteList(ctx context.Context, uid string) ([]string, error) {
	return uc.repo.GetUserFavoriteList(ctx, uid)
}
