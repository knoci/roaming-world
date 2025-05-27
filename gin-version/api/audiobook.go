package api

import (
	"net/http"
	"travel-world/model"
	"travel-world/service"

	"github.com/gin-gonic/gin"
)

type AudiobookController struct {
	audiobookService service.AudiobookService
}

func NewAudiobookController() *AudiobookController {
	return &AudiobookController{
		audiobookService: service.AudiobookService{},
	}
}

// CreateAudiobook 创建有声书
func (ctrl *AudiobookController) CreateAudiobook(c *gin.Context) {
	// 使用密码认证中间件
	var audiobook model.Audiobook
	if err := c.ShouldBindJSON(&audiobook); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	if err := ctrl.audiobookService.CreateAudiobook(&audiobook); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "创建成功",
		"data": audiobook,
	})
}

// CreateAudiobookDetail 创建有声书章节
func (ctrl *AudiobookController) CreateAudiobookDetail(c *gin.Context) {
	var detail model.AudiobookDetail
	if err := c.ShouldBindJSON(&detail); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	if err := ctrl.audiobookService.CreateAudiobookDetail(&detail); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "创建成功",
		"data": detail,
	})
}

// GetAudiobooks 获取所有有声书
func (ctrl *AudiobookController) GetAudiobooks(c *gin.Context) {
	audiobooks, err := ctrl.audiobookService.GetAudiobooks()
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
		"data": audiobooks,
	})
}

// GetAudiobookDetails 获取指定有声书的所有章节
func (ctrl *AudiobookController) GetAudiobookDetails(c *gin.Context) {
	bid := c.Query("bid")
	if bid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "bid参数不能为空",
		})
		return
	}

	details, err := ctrl.audiobookService.GetAudiobookDetails(bid)
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
		"data": details,
	})
}
