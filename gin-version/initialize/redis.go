package initialize

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
)

var RDB *redis.Client

func InitRedis() error {
	redisHost := viper.GetString("redis.host")
	redisPort := viper.GetInt("redis.port")
	redisAddr := fmt.Sprintf("%s:%d", redisHost, redisPort)
	RDB = redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: viper.GetString("redis.password"),
		DB:       viper.GetInt("redis.db"),
	})
	// 测试连接
	ctx := context.Background()
	if err := RDB.Ping(ctx).Err(); err != nil {
		Logger.Error("Redis连接失败", zap.Error(err))
		return err
	}

	Logger.Info("Redis初始化成功")
	return nil
}
