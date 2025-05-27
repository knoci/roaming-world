package api

import (
	"net/http"
	"travel-world/service"

	"github.com/gin-gonic/gin"
)

type FoodController struct {
	foodService *service.FoodService
}

func NewFoodController() *FoodController {
	return &FoodController{
		foodService: service.NewFoodService(),
	}
}

// CreateFood 创建食物
func (ctrl *FoodController) CreateFood(c *gin.Context) {
	var req service.CreateFoodRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误: " + err.Error(),
		})
		return
	}

	food, err := ctrl.foodService.CreateFood(&req)
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
		"data": food,
	})
}

// GetFoodList 获取随机排序的食物列表
func (ctrl *FoodController) GetFoodList(c *gin.Context) {
	foods, err := ctrl.foodService.GetFoodList()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  "获取失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": foods,
	})
}

// GetRandomFood 获取随机单个食物
func (ctrl *FoodController) GetRandomFood(c *gin.Context) {
	food, err := ctrl.foodService.GetRandomFood()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  "获取失败",
		})
		return
	}

	if food == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "没有找到任何食物",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": food,
	})
}
