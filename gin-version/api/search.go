package api

import (
	"net/http"
	"travel-world/initialize"
	"travel-world/service"

	"github.com/gin-gonic/gin"
)

type SearchController struct {
	searchService *service.SearchService
}

func NewSearchController() *SearchController {
	return &SearchController{
		searchService: &service.SearchService{},
	}
}

// SearchArticles 搜索文章
func (ctrl *SearchController) SearchArticles(c *gin.Context) {
	// 获取搜索关键词
	keyword := c.Query("keyword")
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "搜索关键词不能为空",
		})
		return
	}

	// 搜索文章
	articles, err := ctrl.searchService.SearchArticles(initialize.ESClient, keyword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	if len(articles) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"msg":  "未找到相关文章",
			"data": []interface{}{},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "搜索成功",
		"data": articles,
	})
}

// SearchScenes 搜索景点
func (ctrl *SearchController) SearchScenes(c *gin.Context) {
	// 获取搜索关键词
	keyword := c.Query("keyword")
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "搜索关键词不能为空",
		})
		return
	}

	// 搜索景点
	scenes, err := ctrl.searchService.SearchScenes(initialize.ESClient, keyword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	if len(scenes) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"msg":  "未找到相关景点",
			"data": []interface{}{},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "搜索成功",
		"data": scenes,
	})
}
