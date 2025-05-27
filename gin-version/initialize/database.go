package initialize

import (
	"fmt"
	"travel-world/model"

	"go.uber.org/zap"

	"github.com/spf13/viper"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var DB *gorm.DB

func InitDB() error {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s",
		viper.GetString("database.host"),
		viper.GetString("database.username"),
		viper.GetString("database.password"),
		viper.GetString("database.dbname"),
		viper.GetInt("database.port"),
		viper.GetString("database.sslmode"),
	)

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			NameReplacer: nil,
		},
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		Logger.Error("数据库连接失败", zap.Error(err))
		return fmt.Errorf("failed to connect database: %v", err)
	}

	// 自动迁移表结构
	err = DB.AutoMigrate(&model.Food{}, &model.Audiobook{}, &model.AudiobookDetail{}, &model.Scene{}, &model.User{}, &model.Article{}, &model.Favorite{}, &model.Like{}, &model.Comment{}, &model.Commentlike{})
	if err != nil {
		Logger.Error("数据库自动迁移失败", zap.Error(err))
		return fmt.Errorf("failed to auto migrate: %v", err)
	}

	Logger.Info("数据库初始化成功")
	return nil
}
