package service

import (
	"context"
	"errors"
	"time"
	"travel-world/initialize"
	"travel-world/model"

	// "travel-world/pkg/es"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type ArticleService struct{}

// RebuildArticleCache 重建文章的Redis缓存
func (s *ArticleService) RebuildArticleCache() error {
	ctx := context.Background()

	// 从数据库获取所有文章
	var articles []model.Article
	if err := initialize.DB.Find(&articles).Error; err != nil {
		return err
	}

	// 开启Redis事务
	pipeline := initialize.RDB.Pipeline()

	// 清空现有的文章相关缓存
	keys := []string{"article:likes", "article:comments", "article:favorites", "article:time"}
	for _, key := range keys {
		pipeline.Del(ctx, key)
	}

	// 重建缓存数据
	for _, article := range articles {
		// 使用文章表中的统计数据
		likeCount := article.Likes
		commentCount := article.Comments
		favoriteCount := article.Favorite

		// 添加到有序集合
		pipeline.ZAdd(ctx, "article:likes", redis.Z{Score: float64(likeCount), Member: article.AID})
		pipeline.ZAdd(ctx, "article:comments", redis.Z{Score: float64(commentCount), Member: article.AID})
		pipeline.ZAdd(ctx, "article:favorites", redis.Z{Score: float64(favoriteCount), Member: article.AID})
		pipeline.ZAdd(ctx, "article:time", redis.Z{Score: float64(article.CreatedAt.Unix()), Member: article.AID})
	}

	// 执行所有命令
	_, err := pipeline.Exec(ctx)
	if err != nil {
		return err
	}

	initialize.Logger.Info("Successfully rebuilt article cache")
	return nil
}

type CreateArticleRequest struct {
	UID     string   `json:"uid" binding:"required"`
	Title   string   `json:"title" binding:"required,min=1,max=100"`
	View    []string `json:"view" binding:"required,dive,max=200"`
	Content string   `json:"content" binding:"required"`
	Video   bool     `json:"video"`
	Height  int      `json:"height"`
}

type UpdateArticleRequest struct {
	AID     string   `json:"aid" binding:"required"`
	Title   string   `json:"title" binding:"required,min=1,max=100"`
	View    []string `json:"view" binding:"required,dive,max=200"`
	Content string   `json:"content" binding:"required"`
	Video   bool     `json:"video"`
	Height  int      `json:"height"`
}

// CreateArticle 创建文章
func (s *ArticleService) CreateArticle(req *CreateArticleRequest) (*model.Article, error) {
	// 获取用户信息
	var user model.User
	if err := initialize.DB.First(&user, "uid = ?", req.UID).Error; err != nil {
		return nil, err
	}

	// 如果是视频文章，获取当前最大的VideoID并加1
	videoid := 0
	if req.Video {
		var maxVideoID struct {
			MaxID int
		}
		if err := initialize.DB.Model(&model.Article{}).Select("COALESCE(MAX(videoid), 0) as max_id").Where("video = ?", true).Scan(&maxVideoID).Error; err != nil {
			return nil, err
		}
		videoid = maxVideoID.MaxID + 1
	}

	article := &model.Article{
		UID:     req.UID,
		Name:    user.Name,
		Avatar:  user.Avatar,
		Title:   req.Title,
		View:    req.View,
		Content: req.Content,
		Video:   req.Video,
		Videoid: videoid,
		Height:  req.Height,
	}

	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 创建文章记录
	if err := tx.Create(article).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("INSERT INTO articles (aid, uid, name, avatar, title, view, content, video, videoid, height) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', %t, %d, %d)",
		article.AID, article.UID, article.Name, article.Avatar, article.Title, article.View, article.Content, article.Video, article.Videoid, article.Height); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 将文章添加到ES
	// if err := es.InsertArticle(initialize.ESClient, *article); err != nil {
	// 	tx.Rollback()
	// 	initialize.Logger.Error("Failed to insert article to ES", zap.Error(err))
	// 	return nil, err
	// }

	ctx := context.Background()
	// 初始化 Redis 缓存
	pipeline := initialize.RDB.Pipeline()

	// 使用 ZADD 命令将文章添加到各个排序集合中
	pipeline.ZAdd(ctx, "article:likes", redis.Z{Score: 0, Member: article.AID})
	pipeline.ZAdd(ctx, "article:comments", redis.Z{Score: 0, Member: article.AID})
	pipeline.ZAdd(ctx, "article:favorites", redis.Z{Score: 0, Member: article.AID})
	pipeline.ZAdd(ctx, "article:time", redis.Z{Score: float64(time.Now().Unix()), Member: article.AID})

	_, err := pipeline.Exec(ctx)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	return article, nil
}

// UpdateArticle 更新文章
func (s *ArticleService) UpdateArticle(req *UpdateArticleRequest, uid any, avatar any) (*model.Article, error) {
	var article model.Article
	if err := initialize.DB.First(&article, "aid = ?", req.AID).Error; err != nil {
		return nil, errors.New("文章不存在")
	}

	// 检查当前用户是否为文章作者
	if article.UID != uid {
		return nil, errors.New("无权修改他人的文章")
	}

	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 如果视频状态发生变化，需要重新计算VideoID
	if req.Video != article.Video {
		if req.Video {
			// 如果从非视频变为视频，获取新的VideoID
			var maxVideoID struct {
				MaxID int
			}
			if err := tx.Model(&model.Article{}).Select("COALESCE(MAX(video_id), 0) as max_id").Where("video = ?", true).Scan(&maxVideoID).Error; err != nil {
				tx.Rollback()
				return nil, err
			}
			article.Videoid = maxVideoID.MaxID + 1
		} else {
			// 如果从视频变为非视频，设置VideoID为0
			article.Videoid = 0
		}
	}

	article.Avatar = avatar.(string)
	article.Title = req.Title
	article.View = req.View
	article.Content = req.Content
	article.Video = req.Video
	article.Height = req.Height

	if err := tx.Save(&article).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("UPDATE articles SET title = '%s', view = '%s', content = '%s', video = %t, videoid = %d, height = %d WHERE aid = '%s'",
		article.Title, article.View, article.Content, article.Video, article.Videoid, article.Height, article.AID); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 更新ES中的文章
	// if err := es.UpdateArticle(initialize.ESClient, article); err != nil {
	// 	tx.Rollback()
	// 	initialize.Logger.Error("Failed to update article in ES", zap.Error(err))
	// 	return nil, err
	// }

	ctx := context.Background()
	// 更新 Redis 中的时间戳
	if err := initialize.RDB.ZAdd(ctx, "article:time", redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: article.AID,
	}).Err(); err != nil {
		tx.Rollback()
		return nil, err
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	return &article, nil
}

// DeleteArticle 删除文章
func (s *ArticleService) DeleteArticle(aid string, uid any) error {
	// 查询文章信息
	var article model.Article
	if err := initialize.DB.First(&article, "aid = ?", aid).Error; err != nil {
		return errors.New("文章不存在")
	}

	// 检查当前用户是否为文章作者
	if article.UID != uid {
		return errors.New("无权删除他人的文章")
	}

	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 删除数据库中的文章
	if err := tx.Delete(&article).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("DELETE FROM articles WHERE aid = '%s'", aid); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 从ES中删除文章
	// if err := es.DeleteArticle(initialize.ESClient, aid); err != nil {
	// 	tx.Rollback()
	// 	initialize.Logger.Error("Failed to delete article from ES", zap.Error(err))
	// 	return err
	// }

	ctx := context.Background()
	// 删除 Redis 中的相关数据
	pipeline := initialize.RDB.Pipeline()

	// 删除文章在各个排序集合中的数据
	pipeline.ZRem(ctx, "article:likes", aid)
	pipeline.ZRem(ctx, "article:comments", aid)
	pipeline.ZRem(ctx, "article:favorites", aid)
	pipeline.ZRem(ctx, "article:time", aid)

	_, err := pipeline.Exec(ctx)
	if err != nil {
		tx.Rollback()
		return err
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return err
	}

	return nil
}

// LikeArticle 点赞或取消点赞文章
func (s *ArticleService) LikeArticle(aid, uid string) error {
	ctx := context.Background()
	// 更新用户的点赞记录
	var user model.User
	if err := initialize.DB.First(&user, "uid = ?", uid).Error; err != nil {
		return errors.New("用户不存在")
	}

	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 检查数据库中是否已经点赞
	var likeRelation model.Like
	if err := initialize.DB.Where("uid = ? AND aid = ?", uid, aid).First(&likeRelation).Error; err == nil {
		// 如果找到记录，说明已经点赞过，执行取消点赞操作
		if err := tx.Delete(&likeRelation).Error; err != nil {
			tx.Rollback()
			return err
		}

		// 发送SQL日志到Kafka
		if err := initialize.SendSQLLog("DELETE FROM likes WHERE uid = '%s' AND aid = '%s'", uid, aid); err != nil {
			initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
		}

		// 更新文章点赞数
		if err := tx.Model(&model.Article{}).Where("aid = ?", aid).Update("likes", gorm.Expr("likes - ?", 1)).Error; err != nil {
			tx.Rollback()
			return err
		}

		// 更新Redis缓存，直接使用计数器值
		if err := initialize.RDB.ZIncrBy(ctx, "article:likes", -1, aid).Err(); err != nil {
			tx.Rollback()
			return err
		}

		// 提交事务
		if err := tx.Commit().Error; err != nil {
			return err
		}

		// 发送SQL日志到Kafka
		if err := initialize.SendSQLLog("UPDATE articles SET likes = likes - 1 WHERE aid = '%s'", aid); err != nil {
			initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
		}

		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		// 如果是其他错误
		return err
	}

	// 创建点赞关系记录
	likeRelation = model.Like{
		UID: uid,
		AID: aid,
	}
	if err := tx.Create(&likeRelation).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("INSERT INTO likes (uid, aid) VALUES ('%s', '%s')", uid, aid); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 更新文章点赞数
	if err := tx.Model(&model.Article{}).Where("aid = ?", aid).Update("likes", gorm.Expr("likes + ?", 1)).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 更新Redis缓存，直接使用计数器值
	if err := initialize.RDB.ZIncrBy(ctx, "article:likes", 1, aid).Err(); err != nil {
		tx.Rollback()
		return err
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		return err
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("UPDATE articles SET likes = likes + 1 WHERE aid = '%s'", aid); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	return nil
}

// GetUserLikeList 获取用户点赞的文章列表
func (s *ArticleService) GetUserLikeList(uid string) ([]string, error) {
	var relations []model.Like
	if err := initialize.DB.Where("uid = ?", uid).Find(&relations).Error; err != nil {
		return nil, err
	}

	aids := make([]string, len(relations))
	for i, relation := range relations {
		aids[i] = relation.AID
	}

	return aids, nil
}

// GetUserFavoriteList 获取用户收藏的文章列表
func (s *ArticleService) GetUserFavoriteList(uid string) ([]string, error) {
	var relations []model.Favorite
	if err := initialize.DB.Where("uid = ?", uid).Find(&relations).Error; err != nil {
		return nil, err
	}

	aids := make([]string, len(relations))
	for i, relation := range relations {
		aids[i] = relation.AID
	}

	return aids, nil
}

// FavoriteArticle 收藏或取消收藏文章
func (s *ArticleService) FavoriteArticle(aid, uid string) error {
	ctx := context.Background()
	// 更新用户的收藏记录
	var user model.User
	if err := initialize.DB.First(&user, "uid = ?", uid).Error; err != nil {
		return errors.New("用户不存在")
	}

	// 开启事务
	tx := initialize.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 检查数据库中是否已经收藏
	var favoriteRelation model.Favorite
	if err := initialize.DB.Where("uid = ? AND aid = ?", uid, aid).First(&favoriteRelation).Error; err == nil {
		// 如果找到记录，说明已经收藏过，执行取消收藏操作
		if err := tx.Delete(&favoriteRelation).Error; err != nil {
			tx.Rollback()
			return err
		}

		// 发送SQL日志到Kafka
		if err := initialize.SendSQLLog("DELETE FROM favorites WHERE uid = '%s' AND aid = '%s'", uid, aid); err != nil {
			initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
		}

		// 更新文章收藏数
		if err := tx.Model(&model.Article{}).Where("aid = ?", aid).Update("favorite", gorm.Expr("favorite - ?", 1)).Error; err != nil {
			tx.Rollback()
			return err
		}

		// 更新Redis缓存，直接使用计数器值
		if err := initialize.RDB.ZIncrBy(ctx, "article:favorites", -1, aid).Err(); err != nil {
			tx.Rollback()
			return err
		}

		// 提交事务
		if err := tx.Commit().Error; err != nil {
			return err
		}

		// 事务提交成功后发送SQL日志到Kafka
		if err := initialize.SendSQLLog("UPDATE articles SET favorite = favorite - 1 WHERE aid = '%s'", aid); err != nil {
			initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
		}

		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		// 如果是其他错误
		return err
	}

	// 创建收藏关系记录
	favoriteRelation = model.Favorite{
		UID: uid,
		AID: aid,
	}
	if err := tx.Create(&favoriteRelation).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 发送SQL日志到Kafka
	if err := initialize.SendSQLLog("INSERT INTO favorites (uid, aid) VALUES ('%s', '%s')", uid, aid); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	// 更新文章收藏数
	if err := tx.Model(&model.Article{}).Where("aid = ?", aid).Update("favorite", gorm.Expr("favorite + ?", 1)).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 更新Redis缓存，直接使用计数器值
	if err := initialize.RDB.ZIncrBy(ctx, "article:favorites", 1, aid).Err(); err != nil {
		tx.Rollback()
		return err
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		return err
	}

	// 事务提交成功后发送SQL日志到Kafka
	if err := initialize.SendSQLLog("UPDATE articles SET favorite = favorite + 1 WHERE aid = '%s'", aid); err != nil {
		initialize.Logger.Error("Failed to send SQL log to Kafka", zap.Error(err))
	}

	return nil
}

// ListArticles 获取文章列表
func (s *ArticleService) ListArticles(page, limit int, way string, reverse bool) ([]*model.Article, error) {
	ctx := context.Background()
	var key string
	switch way {
	case "likes":
		key = "article:likes"
	case "comments":
		key = "article:comments"
	case "favorites":
		key = "article:favorites"
	case "time":
		key = "article:time"
	default:
		return nil, errors.New("无效的排序方式")
	}

	// 根据排序方式获取文章ID列表
	var result []redis.Z
	var err error
	// 计算分页的起始和结束位置
	start := int64((page - 1) * limit)
	end := int64(page*limit - 1)
	// 当limit为0时，获取所有文章
	if limit == 0 {
		if reverse {
			result, err = initialize.RDB.ZRevRangeWithScores(ctx, key, 0, -1).Result()
		} else {
			result, err = initialize.RDB.ZRangeWithScores(ctx, key, 0, -1).Result()
		}
	} else {
		if reverse {
			result, err = initialize.RDB.ZRevRangeWithScores(ctx, key, start, end).Result()
		} else {
			result, err = initialize.RDB.ZRangeWithScores(ctx, key, start, end).Result()
		}
	}

	// 如果Redis查询失败，从数据库获取数据并重建缓存
	if err != nil {
		initialize.Logger.Error("Failed to get articles from Redis", zap.Error(err))

		// 开启事务
		tx := initialize.DB.Begin()
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()

		// 从数据库获取文章列表
		var articles []*model.Article
		query := tx.Model(&model.Article{})

		// 根据排序方式设置排序条件
		orderBy := ""
		switch way {
		case "likes":
			orderBy = "likes"
		case "comments":
			orderBy = "comments"
		case "favorites":
			orderBy = "favorite"
		case "time":
			orderBy = "created_at"
		}

		// 设置排序方向
		if reverse {
			orderBy += " DESC"
		} else {
			orderBy += " ASC"
		}

		// 执行查询
		if limit > 0 {
			query = query.Offset(int(start)).Limit(int(limit))
		}
		if err := query.Order(orderBy).Find(&articles).Error; err != nil {
			tx.Rollback()
			return nil, err
		}

		// 重建Redis缓存
		pipeline := initialize.RDB.Pipeline()
		for _, article := range articles {
			score := float64(0)
			switch way {
			case "likes":
				score = float64(article.Likes)
			case "comments":
				score = float64(article.Comments)
			case "favorites":
				score = float64(article.Favorite)
			case "time":
				score = float64(article.CreatedAt.Unix())
			}
			pipeline.ZAdd(ctx, key, redis.Z{Score: score, Member: article.AID})
		}

		// 执行Pipeline
		if _, err := pipeline.Exec(ctx); err != nil {
			initialize.Logger.Error("Failed to rebuild Redis cache", zap.Error(err))
		}

		// 提交事务
		if err := tx.Commit().Error; err != nil {
			tx.Rollback()
			return nil, err
		}

		return articles, nil
	}

	// 从Redis获取成功，获取文章详细信息
	articles := make([]*model.Article, 0, len(result))
	for _, z := range result {
		aid := z.Member.(string)
		var article model.Article
		if err := initialize.DB.First(&article, "aid = ?", aid).Error; err != nil {
			continue
		}
		articles = append(articles, &article)
	}

	// 当limit=0时才检查数据一致性，因为用户可能会用limit限制查询数量
	if limit == 0 {
		var totalCount int64
		if err := initialize.DB.Model(&model.Article{}).Count(&totalCount).Error; err != nil {
			return nil, err
		}

		// 如果Redis中的数据不完整，重新从数据库获取并重建缓存
		if len(result) < int(totalCount) {
			initialize.Logger.Warn("Redis cache is incomplete, rebuilding...")
		}

		// 从数据库获取所有文章
		var allArticles []*model.Article
		if err := initialize.DB.Find(&allArticles).Error; err != nil {
			return nil, err
		}

		// 重建Redis缓存
		pipeline := initialize.RDB.Pipeline()
		for _, article := range allArticles {
			score := float64(0)
			switch way {
			case "likes":
				score = float64(article.Likes)
			case "comments":
				score = float64(article.Comments)
			case "favorites":
				score = float64(article.Favorite)
			case "time":
				score = float64(article.CreatedAt.Unix())
			}
			pipeline.ZAdd(ctx, key, redis.Z{Score: score, Member: article.AID})
		}

		// 执行Pipeline
		if _, err := pipeline.Exec(ctx); err != nil {
			initialize.Logger.Error("Failed to rebuild Redis cache", zap.Error(err))
		}

		// 重新获取文章列表
		if limit == 0 {
			if reverse {
				result, err = initialize.RDB.ZRevRangeWithScores(ctx, key, 0, -1).Result()
			} else {
				result, err = initialize.RDB.ZRangeWithScores(ctx, key, 0, -1).Result()
			}
		} else {
			if reverse {
				result, err = initialize.RDB.ZRevRangeWithScores(ctx, key, start, end).Result()
			} else {
				result, err = initialize.RDB.ZRangeWithScores(ctx, key, start, end).Result()
			}
		}

		if err != nil {
			return nil, err
		}

		// 重新获取文章详细信息
		articles = make([]*model.Article, 0, len(result))
		for _, z := range result {
			aid := z.Member.(string)
			var article model.Article
			if err := initialize.DB.First(&article, "aid = ?", aid).Error; err != nil {
				continue
			}
			articles = append(articles, &article)
		}
	}

	return articles, nil
}

// FindArticleByID 根据文章ID查找文章
func (s *ArticleService) FindArticleByID(aid string) (*model.Article, error) {
	var article model.Article

	// 从数据库中查找文章
	if err := initialize.DB.First(&article, "aid = ?", aid).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New("文章不存在")
		}
		return nil, err
	}

	return &article, nil
}

// GetVideoArticle 根据VideoID获取视频文章
func (s *ArticleService) GetVideoArticle(videoid int) (*model.Article, error) {
	var article model.Article
	if err := initialize.DB.Where("video = ? AND videoid = ?", true, videoid).First(&article).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New("视频文章不存在")
		}
		return nil, err
	}

	return &article, nil
}

// GetMaxVideoID 获取最大的视频ID
func (s *ArticleService) GetMaxVideoID() (int, error) {
	var maxVideoID struct {
		MaxID int
	}
	if err := initialize.DB.Model(&model.Article{}).Select("COALESCE(MAX(videoid), 0) as max_id").Where("video = ?", true).Scan(&maxVideoID).Error; err != nil {
		return 0, err
	}
	return maxVideoID.MaxID, nil
}
