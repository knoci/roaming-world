package service

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/transport/http"
	pb "github.com/knoci/roaming-world/article/api/article/v1"
	"github.com/knoci/roaming-world/article/internal/biz"
	"github.com/knoci/roaming-world/article/internal/pkg"
)

func NewArticleService(uc *biz.ArticleUsecase, logger log.Logger) *ArticleService {
	return &ArticleService{uc: uc, log: log.NewHelper(logger)}
}

func (s *ArticleService) CreateArticle(ctx context.Context, req *pb.CreateArticleRequest) (*pb.CreateArticleReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	article, err := s.uc.Create(ctx, claims.UID, claims.Name, claims.Avatar, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("articleService: CreateArticle failed: %v", err)
		return nil, err
	}
	return &pb.CreateArticleReply{Article: toMessage(article)}, nil
}

func (s *ArticleService) UpdateArticle(ctx context.Context, req *pb.UpdateArticleRequest) (*pb.UpdateArticleReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	article, err := s.uc.Update(ctx, claims.UID, claims.Avatar, req)
	if err != nil {
		s.log.WithContext(ctx).Errorf("articleService: UpdateArticle failed: %v", err)
		return nil, err
	}
	return &pb.UpdateArticleReply{Article: toMessage(article)}, nil
}

func (s *ArticleService) DeleteArticle(ctx context.Context, req *pb.DeleteArticleRequest) (*pb.DeleteArticleReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	if err := s.uc.Delete(ctx, claims.UID, req.Aid); err != nil {
		s.log.WithContext(ctx).Errorf("articleService: DeleteArticle failed: %v", err)
		return nil, err
	}
	return &pb.DeleteArticleReply{Status: "删除成功"}, nil
}

func (s *ArticleService) LikeArticle(ctx context.Context, req *pb.LikeArticleRequest) (*pb.LikeArticleReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	liked, likes, err := s.uc.Like(ctx, claims.UID, req.Aid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("articleService: LikeArticle failed: %v", err)
		return nil, err
	}
	return &pb.LikeArticleReply{Liked: liked, Likes: int32(likes)}, nil
}

func (s *ArticleService) FavoriteArticle(ctx context.Context, req *pb.FavoriteArticleRequest) (*pb.FavoriteArticleReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	favorited, favorite, err := s.uc.Favorite(ctx, claims.UID, req.Aid)
	if err != nil {
		s.log.WithContext(ctx).Errorf("articleService: FavoriteArticle failed: %v", err)
		return nil, err
	}
	return &pb.FavoriteArticleReply{Favorited: favorited, Favorite: int32(favorite)}, nil
}

func (s *ArticleService) ListArticles(ctx context.Context, req *pb.ListArticlesRequest) (*pb.ListArticlesReply, error) {
	articles, total, err := s.uc.List(ctx, int(req.Page), int(req.Limit), req.Way, req.Reverse)
	if err != nil {
		s.log.WithContext(ctx).Errorf("articleService: ListArticles failed: %v", err)
		return nil, err
	}
	reply := &pb.ListArticlesReply{Total: total, Articles: make([]*pb.ArticleMessage, 0, len(articles))}
	for _, article := range articles {
		reply.Articles = append(reply.Articles, toMessage(article))
	}
	return reply, nil
}

func (s *ArticleService) GetArticle(ctx context.Context, req *pb.GetArticleRequest) (*pb.GetArticleReply, error) {
	article, err := s.uc.FindByID(ctx, req.Aid)
	if err != nil {
		return nil, err
	}
	return &pb.GetArticleReply{Article: toMessage(article)}, nil
}

func (s *ArticleService) GetUserLikeList(ctx context.Context, req *pb.GetUserLikeListRequest) (*pb.GetUserLikeListReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	aids, err := s.uc.GetUserLikeList(ctx, claims.UID)
	if err != nil {
		return nil, biz.ErrInternalError
	}
	return &pb.GetUserLikeListReply{Aids: aids}, nil
}

func (s *ArticleService) GetUserFavoriteList(ctx context.Context, req *pb.GetUserFavoriteListRequest) (*pb.GetUserFavoriteListReply, error) {
	claims, err := GetJwtClaim(ctx)
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	aids, err := s.uc.GetUserFavoriteList(ctx, claims.UID)
	if err != nil {
		return nil, biz.ErrInternalError
	}
	return &pb.GetUserFavoriteListReply{Aids: aids}, nil
}

func (s *ArticleService) GetVideoArticle(ctx context.Context, req *pb.GetVideoArticleRequest) (*pb.GetVideoArticleReply, error) {
	article, err := s.uc.GetVideoArticle(ctx, int(req.Videoid))
	if err != nil {
		return nil, err
	}
	return &pb.GetVideoArticleReply{Article: toMessage(article)}, nil
}

func (s *ArticleService) GetMaxVideoID(ctx context.Context, req *pb.GetMaxVideoIDRequest) (*pb.GetMaxVideoIDReply, error) {
	maxID, err := s.uc.GetMaxVideoID(ctx)
	if err != nil {
		return nil, biz.ErrInternalError
	}
	return &pb.GetMaxVideoIDReply{Videoid: int32(maxID)}, nil
}

// GetJwtClaim 同时兼容 HTTP 和 gRPC：HTTP 从请求头取 token，gRPC 从 metadata 取。
// 迁移期网关会把 Authorization 和 User-Agent 透传到内部服务。
func GetJwtClaim(ctx context.Context) (*pkg.Claims, error) {
	var authHeader, ua string
	if req, ok := http.RequestFromServerContext(ctx); ok && req != nil {
		authHeader = req.Header.Get("Authorization")
		ua = req.Header.Get("User-Agent")
	} else if md, ok := metadata.FromServerContext(ctx); ok {
		authHeader = md.Get("authorization")
		ua = md.Get("user-agent")
	}

	if authHeader == "" {
		return nil, biz.ErrUnauthorized
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return nil, biz.ErrUnauthorized
	}
	claims, err := pkg.ParseToken(ua, parts[1])
	if err != nil {
		return nil, biz.ErrUnauthorized
	}
	return claims, nil
}

func toMessage(a *biz.Article) *pb.ArticleMessage {
	if a == nil {
		return nil
	}
	return &pb.ArticleMessage{
		Aid:       a.AID,
		Uid:       a.UID,
		Name:      a.Name,
		Avatar:    a.Avatar,
		Title:     a.Title,
		View:      a.View,
		Content:   a.Content,
		Likes:     int32(a.Likes),
		Comments:  int32(a.Comments),
		Favorite:  int32(a.Favorite),
		Video:     a.Video,
		Videoid:   int32(a.Videoid),
		Height:    int32(a.Height),
		CreatedAt: a.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt: a.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
}
