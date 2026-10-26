package conf

import (
	knacos "github.com/go-kratos/kratos/contrib/config/nacos/v2"
	registry "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/nacos-group/nacos-sdk-go/clients"
	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"github.com/nacos-group/nacos-sdk-go/vo"
	"github.com/spf13/viper"
)

// Nacos 连接信息。迁移期从本地 config.yaml 读取，再通过 Nacos 拉取业务配置。
var (
	nacosConfig      config.Config
	nacosIp          string
	nacosPort        uint64
	nacosNameSpaceId string
	nacosDataId      string
	nacosGroup       string
)

func readConfig() {
	localConfig := viper.New()
	// 兼容从 cmd/<service> 目录、模块根目录以及容器内 /app 启动。
	for _, path := range []string{"../../configs/config.yaml", "./configs/config.yaml", "../configs/config.yaml", "/app/configs/config.yaml"} {
		localConfig.SetConfigFile(path)
		if err := localConfig.ReadInConfig(); err == nil {
			nacosIp = localConfig.GetString("data.nacos.addr")
			nacosPort = localConfig.GetUint64("data.nacos.port")
			nacosNameSpaceId = localConfig.GetString("data.nacos.namespaceId")
			nacosDataId = localConfig.GetString("data.nacos.dataId")
			nacosGroup = localConfig.GetString("data.nacos.groupId")
			return
		}
	}
	panic("nacos: config.yaml not found")
}

func InitNacosConfig() config.Config {
	readConfig()

	sc := []constant.ServerConfig{
		*constant.NewServerConfig(nacosIp, nacosPort),
	}
	cc := &constant.ClientConfig{
		NamespaceId:         nacosNameSpaceId,
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		LogDir:              "./tmp/nacos/log",
		CacheDir:            "./tmp/nacos/cache",
		LogLevel:            "debug",
	}

	client, err := clients.NewConfigClient(
		vo.NacosClientParam{
			ClientConfig:  cc,
			ServerConfigs: sc,
		},
	)
	if err != nil {
		panic(err)
	}

	c := config.New(
		config.WithSource(
			knacos.NewConfigSource(client,
				knacos.WithGroup(nacosGroup),
				knacos.WithDataID(nacosDataId),
			)))
	if err := c.Load(); err != nil {
		panic(err)
	}
	return c
}

func GetConfig() config.Config {
	if nacosConfig == nil {
		log.Info("nacos: initializing remote config")
		nacosConfig = InitNacosConfig()
	}
	return nacosConfig
}

func GetConfigString(cfg config.Config, key string) string {
	value, err := cfg.Value(key).String()
	if err != nil {
		log.Fatalf("nacos: failed to get config %s: %v", key, err)
	}
	return value
}

func GetConfigInt(cfg config.Config, key string) int {
	value, err := cfg.Value(key).Int()
	if err != nil {
		log.Fatalf("nacos: failed to get config %s: %v", key, err)
	}
	return int(value)
}

// NewRegistry 创建 Nacos 服务注册器，供 kratos.Registrar 使用。
func NewRegistry() *registry.Registry {
	if nacosIp == "" {
		readConfig()
	}
	sc := []constant.ServerConfig{
		*constant.NewServerConfig(nacosIp, nacosPort),
	}
	cc := constant.ClientConfig{
		NamespaceId:         nacosNameSpaceId,
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		LogDir:              "./tmp/nacos/log",
		CacheDir:            "./tmp/nacos/cache",
		LogLevel:            "debug",
	}
	client, err := clients.NewNamingClient(
		vo.NacosClientParam{
			ClientConfig:  &cc,
			ServerConfigs: sc,
		},
	)
	if err != nil {
		panic(err)
	}
	return registry.New(client)
}
