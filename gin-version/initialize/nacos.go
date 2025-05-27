package initialize

import (
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"github.com/spf13/viper"
)

var NacosClient config_client.IConfigClient

func InitNacos() error {
	// 创建 Nacos 配置客户端
	sc := []constant.ServerConfig{
		{
			IpAddr: viper.GetString("nacos.host"),
			Port:   uint64(viper.GetInt("nacos.port")),
		},
	}

	cc := constant.ClientConfig{
		NamespaceId:         viper.GetString("nacos.namespace"),
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		LogDir:              "/tmp/nacos/log",
		CacheDir:            "/tmp/nacos/cache",
		LogLevel:            "debug",
	}

	client, err := clients.CreateConfigClient(map[string]interface{}{
		"serverConfigs": sc,
		"clientConfig":  cc,
	})

	if err != nil {
		Logger.Error("创建 Nacos 客户端失败", zap.Error(err))
		return fmt.Errorf("创建 Nacos 客户端失败: %v", err)
	}

	NacosClient = client

	// 获取配置
	content, err := client.GetConfig(vo.ConfigParam{
		DataId: viper.GetString("nacos.dataId"),
		Group:  viper.GetString("nacos.group"),
	})

	if err != nil {
		Logger.Error("获取 Nacos 配置失败", zap.Error(err))
		return fmt.Errorf("获取 Nacos 配置失败: %v", err)
	}

	// 将配置内容设置到 viper
	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(strings.NewReader(content)); err != nil {
		Logger.Error("解析 Nacos 配置失败", zap.Error(err))
		return fmt.Errorf("解析 Nacos 配置失败: %v", err)
	}

	Logger.Info("Nacos初始化成功")
	return nil
}
