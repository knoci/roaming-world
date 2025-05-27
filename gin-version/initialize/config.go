package initialize

import (
	"fmt"
	"go.uber.org/zap"

	"github.com/spf13/viper"
)

func InitConfig() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./configs")

	if err := viper.ReadInConfig(); err != nil {
		Logger.Error("配置文件读取失败", zap.Error(err))
		return fmt.Errorf("failed to read config file: %v", err)
	}

	Logger.Info("配置初始化成功")
	return nil
}
