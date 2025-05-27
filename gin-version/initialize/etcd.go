package initialize

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"time"

	"github.com/spf13/viper"
	"go.etcd.io/etcd/client/v3"
)

var EtcdClient *clientv3.Client

func InitEtcd() error {
	// 从viper获取etcd配置
	endpoints := viper.GetStringSlice("etcd.endpoints")
	dialTimeout := viper.GetDuration("etcd.dialTimeout")

	if len(endpoints) == 0 {
		Logger.Error("etcd endpoints未配置")
		return fmt.Errorf("etcd endpoints not configured")
	}

	// 创建etcd客户端
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout * time.Second,
	})

	if err != nil {
		Logger.Error("创建etcd客户端失败", zap.Error(err))
		return fmt.Errorf("创建etcd客户端失败: %v", err)
	}

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.Status(ctx, endpoints[0])
	if err != nil {
		Logger.Error("连接etcd失败", zap.Error(err))
		return fmt.Errorf("连接etcd失败: %v", err)
	}

	EtcdClient = client
	Logger.Info("Etcd初始化成功")
	return nil
}
