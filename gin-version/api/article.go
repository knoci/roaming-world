package api

import (
	"net/http"
	"strconv"
	"travel-world/service"

	"github.com/gin-gonic/gin"
)

type ArticleController struct {
	articleService service.ArticleService
}

func NewArticleController() *ArticleController {
	return &ArticleController{
		articleService: service.ArticleService{},
	}
}

// CreateArticle 创建文章
func (ctrl *ArticleController) CreateArticle(c *gin.Context) {
	var req service.CreateArticleRequest

	// 从上下文获取用户ID
	uid, _ := c.Get("uid")
	req.UID = uid.(string)

	// 绑定请求参数
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	// 创建文章
	article, err := ctrl.articleService.CreateArticle(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "创建成功",
		"data": article,
	})
}

// UpdateArticle 更新文章
func (ctrl *ArticleController) UpdateArticle(c *gin.Context) {
	var req service.UpdateArticleRequest

	// 从上下文获取用户ID
	uid, _ := c.Get("uid")
	avatar, _ := c.Get("avatar")

	// 绑定请求参数
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	// 更新文章
	article, err := ctrl.articleService.UpdateArticle(&req, uid, avatar)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "更新成功",
		"data": article,
	})
}

// GetUserLikeList 获取用户点赞的文章列表
func (ctrl *ArticleController) GetUserLikeList(c *gin.Context) {
	// 从上下文获取用户ID
	uid, _ := c.Get("uid")

	// 获取用户点赞列表
	aids, err := ctrl.articleService.GetUserLikeList(uid.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": aids,
	})
}

// GetUserFavoriteList 获取用户收藏的文章列表
func (ctrl *ArticleController) GetUserFavoriteList(c *gin.Context) {
	// 从上下文获取用户ID
	uid, _ := c.Get("uid")

	// 获取用户收藏列表
	aids, err := ctrl.articleService.GetUserFavoriteList(uid.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": aids,
	})
}

// DeleteArticle 删除文章
func (ctrl *ArticleController) DeleteArticle(c *gin.Context) {
	// 从上下文获取用户ID
	uid, _ := c.Get("uid")
	aid := c.Query("aid")
	if aid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "缺少aid参数",
		})
		return
	}

	// 删除文章
	if err := ctrl.articleService.DeleteArticle(aid, uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "删除成功",
	})
}

// LikeArticle 点赞文章
func (ctrl *ArticleController) LikeArticle(c *gin.Context) {
	aid := c.Query("aid")
	if aid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "缺少aid参数",
		})
		return
	}

	// 从上下文获取用户ID
	uid, _ := c.Get("uid")

	// 点赞文章
	if err := ctrl.articleService.LikeArticle(aid, uid.(string)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "点赞成功",
	})
}

// FavoriteArticle 收藏文章
func (ctrl *ArticleController) FavoriteArticle(c *gin.Context) {
	aid := c.Query("aid")
	if aid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "缺少aid参数",
		})
		return
	}

	// 从上下文获取用户ID
	uid, _ := c.Get("uid")

	// 收藏文章
	if err := ctrl.articleService.FavoriteArticle(aid, uid.(string)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "收藏成功",
	})
}

// ListArticles 获取文章列表
func (ctrl *ArticleController) ListArticles(c *gin.Context) {
	// 获取查询参数
	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.Query("limit")

	// 解析page参数
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "page参数格式错误",
		})
		return
	}

	// 解析limit参数
	var limit int
	if limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 400,
				"msg":  "limit参数格式错误",
			})
			return
		}
	}
	way := c.DefaultQuery("way", "time")
	reverse, _ := strconv.ParseBool(c.DefaultQuery("reverse", "true"))

	// 获取文章列表
	articles, err := ctrl.articleService.ListArticles(page, limit, way, reverse)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": articles,
	})
}

// FindArticles 根据文章ID查找文章
func (ctrl *ArticleController) FindArticles(c *gin.Context) {
	// 获取aid参数
	aid := c.Query("aid")
	if aid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "文章ID不能为空",
		})
		return
	}

	// 查找文章
	article, err := ctrl.articleService.FindArticleByID(aid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "查询成功",
		"data": article,
	})
}

// GetVideoArticles 根据VideoID获取视频类型的文章
func (ctrl *ArticleController) GetVideoArticles(c *gin.Context) {
	// 获取videoID参数
	videoIDStr := c.Query("id")
	if videoIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "缺少video_id参数",
		})
		return
	}

	// 将videoID转换为整数
	videoID, err := strconv.Atoi(videoIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "videoid参数格式错误",
		})
		return
	}

	// 获取视频文章
	article, err := ctrl.articleService.GetVideoArticle(videoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": article,
	})
}

// GetMaxVideoID 获取最大的视频ID
func (ctrl *ArticleController) GetMaxVideoID(c *gin.Context) {
	// 获取最大的视频ID
	maxID, err := ctrl.articleService.GetMaxVideoID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": maxID,
	})
}
