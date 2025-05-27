package api

import (
	"net/http"
	"travel-world/service"

	"github.com/gin-gonic/gin"
)

type CommentController struct {
	commentService *service.CommentService
}

func NewCommentController() *CommentController {
	return &CommentController{
		commentService: &service.CommentService{},
	}
}

// CreateComment 发表评论
func (api *CommentController) CreateComment(c *gin.Context) {
	var comment struct {
		Target    string `json:"target" binding:"required"`  // 文章AID
		Content   string `json:"content" binding:"required"` // 评论内容
		Replycid  string `json:"replycid"`                   // 回复的评论CID
		Replyname string `json:"replyname"`                  // 回复的用户名
	}

	if err := c.ShouldBindJSON(&comment); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}

	uid := c.GetString("uid")
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 401,
			"msg":  "未登录",
		})
		return
	}

	result, err := api.commentService.CreateComment(uid, comment.Target, comment.Content, comment.Replycid, comment.Replyname)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "评论成功",
		"data": result,
	})
}

// DeleteComment 删除评论
func (api *CommentController) DeleteComment(c *gin.Context) {
	cid := c.Query("cid")
	if cid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}

	uid := c.GetString("uid")
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 401,
			"msg":  "未登录",
		})
		return
	}

	err := api.commentService.DeleteComment(uid, cid)
	if err != nil {
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

// LikeComment 点赞评论
func (api *CommentController) LikeComment(c *gin.Context) {
	cid := c.Query("cid")
	if cid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}

	uid := c.GetString("uid")
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 401,
			"msg":  "未登录",
		})
		return
	}

	err := api.commentService.LikeComment(uid, cid)
	if err != nil {
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

// GetCommentList 获取一级评论列表
func (api *CommentController) GetCommentList(c *gin.Context) {
	aid := c.Query("aid")
	if aid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}

	result, err := api.commentService.GetCommentList(aid)
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
		"data": result,
	})
}

// GetCommentList 获取一级评论列表
func (api *CommentController) GetCommentList2(c *gin.Context) {
	aid := c.Query("aid")
	if aid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}

	result, err := api.commentService.GetCommentListWithReplies(aid)
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
		"data": result,
	})
}

// GetReplyList 获取二级评论列表
func (api *CommentController) GetReplyList(c *gin.Context) {
	cid := c.Query("cid")
	if cid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}

	result, err := api.commentService.GetReplyList(cid)
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
		"data": result,
	})
}
