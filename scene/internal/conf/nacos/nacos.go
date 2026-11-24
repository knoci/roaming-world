package conf

import (
	registry "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/knoci/roaming-world/pkg/nacosx"
)

func InitNacosConfig() config.Config {
	return nacosx.InitNacosConfig()
}

func GetConfig() config.Config {
	return nacosx.GetConfig()
}

func GetConfigString(cfg config.Config, key string) string {
	return nacosx.MustGetString(cfg, key)
}

func GetConfigInt(cfg config.Config, key string) int {
	return nacosx.MustGetInt(cfg, key)
}

// NewRegistry 创建 Nacos 服务注册器，供 kratos.Registrar 使用。
func NewRegistry() *registry.Registry {
	return nacosx.NewRegistry()
}
