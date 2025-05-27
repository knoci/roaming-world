package api

import (
	"net/http"
	"strconv"
	"travel-world/model"
	"travel-world/service"

	"github.com/gin-gonic/gin"
)

type SceneController struct {
	sceneService *service.SceneService
}

func NewSceneController() *SceneController {
	return &SceneController{
		sceneService: service.NewSceneService(),
	}
}

func (ctrl *SceneController) CreateScene(c *gin.Context) {
	var req service.SceneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	scene, err := ctrl.sceneService.CreateScene(c.Request.Context(), &req)
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
		"data": scene,
	})
}

func (ctrl *SceneController) DeleteScene(c *gin.Context) {
	sid := c.PostForm("sid")
	if sid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "sid不能为空",
		})
		return
	}

	if err := ctrl.sceneService.DeleteScene(c.Request.Context(), sid); err != nil {
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

func (ctrl *SceneController) UpdateScene(c *gin.Context) {
	var scene model.Scene
	if err := c.ShouldBindJSON(&scene); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	if scene.SID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "sid不能为空",
		})
		return
	}

	if err := ctrl.sceneService.UpdateScene(c.Request.Context(), &scene); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "更新成功",
		"data": scene,
	})
}

func (ctrl *SceneController) SearchScene(c *gin.Context) {
	keyword := c.Query("keyword")
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "关键词不能为空",
		})
		return
	}

	scenes, err := ctrl.sceneService.SearchScene(c.Request.Context(), keyword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	if len(scenes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "没有数据",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "success",
		"data": scenes,
	})
}

func (ctrl *SceneController) ListScenes(c *gin.Context) {
	limitStr := c.Query("limit")
	var limit int
	if limitStr != "" {
		var err error
		limit, err = strconv.Atoi(limitStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 400,
				"msg":  "limit参数格式错误",
			})
			return
		}
	}

	scenes, err := ctrl.sceneService.ListScenes(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "success",
		"data": scenes,
	})
}

func (ctrl *SceneController) GetSceneByID(c *gin.Context) {
	sid := c.Query("sid")
	if sid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "sid不能为空",
		})
		return
	}

	scene, err := ctrl.sceneService.GetSceneByID(c.Request.Context(), sid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	if scene == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "场景不存在",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "success",
		"data": scene,
	})
}
