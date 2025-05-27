package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"travel-world/api"
	"travel-world/initialize"
	"travel-world/middleware"
	"travel-world/pkg/email"
	"travel-world/service"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

func main() {
	// 初始化日志系统
	if err := initialize.InitLogger(); err != nil {
		initialize.Logger.Fatal("Logger initialization failed", zap.Error(err))
	}

	// 初始化配置
	if err := initialize.InitConfig(); err != nil {
		initialize.Logger.Fatal("Config initialization failed", zap.Error(err))
	}

	// 初始化 Nacos
	if err := initialize.InitNacos(); err != nil {
		initialize.Logger.Fatal("Nacos initialization failed", zap.Error(err))
	}

	// 初始化数据库
	if err := initialize.InitDB(); err != nil {
		initialize.Logger.Fatal("Database initialization failed", zap.Error(err))
	}

	// 初始化 Kafka
	if err := initialize.InitKafka(); err != nil {
		initialize.Logger.Fatal("Kafka initialization failed", zap.Error(err))
	}

	// 启动后台协程，每2小时检查并重建Kafka连接
	go func() {
		for {
			time.Sleep(2 * time.Hour)
			initialize.Logger.Info("开始检查Kafka连接状态")
			if err := initialize.ReconnectKafka(); err != nil {
				initialize.Logger.Error("重建Kafka连接失败", zap.Error(err))
			} else {
				initialize.Logger.Info("重建Kafka连接成功")
			}
		}
	}()

	// 初始化邮箱
	email.InitEmailConfig()

	// 初始化 etcd
	if err := initialize.InitEtcd(); err != nil {
		initialize.Logger.Fatal("Etcd initialization failed", zap.Error(err))
	}

	// 初始化 Redis
	if err := initialize.InitRedis(); err != nil {
		initialize.Logger.Fatal("Redis initialization failed", zap.Error(err))
	}

	// 初始化腾讯云COS
	if err := initialize.InitCOS(); err != nil {
		initialize.Logger.Fatal("COS initialization failed", zap.Error(err))
	}

	// 启动后台协程，每半小时更新用户头像
	go func() {
		userService := service.UserService{}
		userService.UpdateAvatar()
	}()

	// 启动后台协程，每小时重建文章的Redis缓存
	go func() {
		articleService := service.ArticleService{}
		for {
			// 重建文章缓存
			if err := articleService.RebuildArticleCache(); err != nil {
				initialize.Logger.Error("Failed to rebuild article cache", zap.Error(err))
			}
			// 等待2小时
			time.Sleep(12 * time.Hour)
		}
	}()

	// 启动后台协程，每2小时重建有声书的Redis缓存
	go func() {
		audiobookService := service.AudiobookService{}
		for {
			// 重建有声书缓存
			if err := audiobookService.RebuildAudiobookCache(); err != nil {
				initialize.Logger.Error("Failed to rebuild audiobook cache", zap.Error(err))
			}
			// 等待
			time.Sleep(36 * time.Hour)
		}
	}()

	// 初始化es
	// if err := initialize.InitES(); err != nil {
	// 	initialize.Logger.Fatal("ES initialization failed", zap.Error(err))
	// }

	// 设置gin模式
	gin.SetMode(viper.GetString("server.mode"))

	// 创建路由引擎
	r := gin.Default()

	// 创建控制器
	userController := api.NewUserController()
	sceneController := api.NewSceneController()
	articleController := api.NewArticleController()
	// searchController := api.NewSearchController()
	foodController := api.NewFoodController()
	commentController := api.NewCommentController()
	audiobookController := api.NewAudiobookController()

	// 注册路由
	userRouter := r.Group("/user")
	{
		// 公开路由
		userRouter.POST("/register", userController.Register)
		userRouter.POST("/register/verify", userController.VerifyAndCompleteRegistration)
		userRouter.POST("/login", userController.Login)
		userRouter.GET("/find", userController.Find)
		userRouter.POST("/confirm", userController.CheckUserExist)
		userRouter.POST("/reset", userController.ResetPassword)
	}

	// 评论路由组
	commentGroup := r.Group("/comments")
	{
		// 不需要认证的路由
		commentGroup.GET("/list", commentController.GetCommentList)
		commentGroup.GET("/listwith", commentController.GetCommentList2)
		commentGroup.GET("/findreply", commentController.GetReplyList)
	}

	// 需要认证的评论路由
	authorizedCommentGroup := r.Group("/comment").Use(middleware.AuthUserMiddleware())
	{
		authorizedCommentGroup.POST("/commit", commentController.CreateComment)
		authorizedCommentGroup.POST("/like", commentController.LikeComment)
		authorizedCommentGroup.POST("/delete", commentController.DeleteComment)
	}

	// 需要认证的用户路由
	authorizedUserRouter := r.Group("/userdue").Use(middleware.AuthUserMiddleware())
	{
		authorizedUserRouter.POST("/delete", userController.Delete)
		authorizedUserRouter.POST("/postavatar", userController.PostAvatar)
		authorizedUserRouter.POST("/verify", userController.VerifyEmail)
		authorizedUserRouter.POST("/update", userController.UpdateUserInfo)
	}

	// 注册场景路由
	sceneGroup := r.Group("/scenes")
	{
		// 不需要认证的路由
		sceneGroup.GET("/search", sceneController.SearchScene)
		sceneGroup.GET("/list", sceneController.ListScenes)
		sceneGroup.GET("/searchto", sceneController.GetSceneByID)
	}

	// 需要认证的场景路由
	authorizedSceneGroup := r.Group("/scene").Use(middleware.AuthPasswordMiddleware())
	{
		authorizedSceneGroup.POST("/create", sceneController.CreateScene)
		authorizedSceneGroup.POST("/delete", sceneController.DeleteScene)
		authorizedSceneGroup.POST("/update", sceneController.UpdateScene)
	}

	// 文章路由组
	articleGroup := r.Group("/articles")
	{
		// 不需要认证的路由
		articleGroup.GET("/list", articleController.ListArticles)
		articleGroup.GET("/find", articleController.FindArticles)
		articleGroup.GET("/video", articleController.GetVideoArticles)
		articleGroup.GET("/count", articleController.GetMaxVideoID)
	}

	// 需要认证的文章路由
	authorizedArticleGroup := r.Group("/article").Use(middleware.AuthUserMiddleware())
	{
		authorizedArticleGroup.POST("/create", articleController.CreateArticle)
		authorizedArticleGroup.POST("/update", articleController.UpdateArticle)
		authorizedArticleGroup.POST("/delete", articleController.DeleteArticle)
		authorizedArticleGroup.POST("/like", articleController.LikeArticle)
		authorizedArticleGroup.POST("/favorite", articleController.FavoriteArticle)
		authorizedArticleGroup.GET("/like/list", articleController.GetUserLikeList)
		authorizedArticleGroup.GET("/favorite/list", articleController.GetUserFavoriteList)
	}

	// 搜索路由组
	// searchGroup := r.Group("/search")
	// {
	// 	searchGroup.GET("/articles", searchController.SearchArticles)
	// 	searchGroup.GET("/scenes", searchController.SearchScenes)
	// }

	// 食物路由组
	foodGroup := r.Group("/foods")
	{
		// 不需要认证的路由
		foodGroup.GET("/list", foodController.GetFoodList)
		foodGroup.GET("/rand", foodController.GetRandomFood)
	}

	// 需要认证的食物路由
	authorizedFoodGroup := r.Group("/food").Use(middleware.AuthPasswordMiddleware())
	{
		authorizedFoodGroup.POST("/create", foodController.CreateFood)
	}

	// 有声书路由组
	audiobookGroup := r.Group("/audiobooks")
	{
		// 不需要认证的路由
		audiobookGroup.GET("/list", audiobookController.GetAudiobooks)
		audiobookGroup.GET("/details", audiobookController.GetAudiobookDetails)
	}

	// 需要认证的有声书路由
	authorizedAudiobookGroup := r.Group("/audiobook").Use(middleware.AuthPasswordMiddleware())
	{
		authorizedAudiobookGroup.POST("/create", audiobookController.CreateAudiobook)
		authorizedAudiobookGroup.POST("/detail/create", audiobookController.CreateAudiobookDetail)
	}

	// 配置静态文件服务
	r.Static("/static", "./index")

	// 创建一个通道来接收系统信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 启动服务器
	port := viper.GetString("server.port")
	server := &http.Server{
		Addr:    fmt.Sprintf(":%s", port),
		Handler: r,
	}

	// 在一个新的goroutine中启动服务器
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			initialize.Logger.Fatal("Server startup failed", zap.Error(err))
		}
	}()

	initialize.Logger.Info("Server started successfully")

	// 等待中断信号
	<-sigChan
	initialize.Logger.Info("Shutting down server...")

	// 创建一个带有超时的上下文
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 优雅地关闭服务器
	if err := server.Shutdown(ctx); err != nil {
		initialize.Logger.Error("Server forced to shutdown", zap.Error(err))
	}

	// 关闭其他资源
	if err := initialize.DB.WithContext(ctx).Error; err != nil {
		initialize.Logger.Error("Error closing DB connection", zap.Error(err))
	}

	if err := initialize.RDB.Close(); err != nil {
		initialize.Logger.Error("Error closing Redis connection", zap.Error(err))
	}

	if err := initialize.EtcdClient.Close(); err != nil {
		initialize.Logger.Error("Error closing Etcd connection", zap.Error(err))
	}

	initialize.Logger.Info("Server exited properly")
}
