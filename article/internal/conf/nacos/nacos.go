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

func GetConfigStringOrDefault(cfg config.Config, key, def string) string {
	return nacosx.GetStringOrDefault(cfg, key, def)
}

func GetConfigInt(cfg config.Config, key string) int {
	return nacosx.MustGetInt(cfg, key)
}

func GetConfigIntOrDefault(cfg config.Config, key string, def int) int {
	return nacosx.GetIntOrDefault(cfg, key, def)
}

// NewRegistry 创建 Nacos 服务注册器，供 kratos.Registrar 使用。
func NewRegistry() *registry.Registry {
	return nacosx.NewRegistry()
}
