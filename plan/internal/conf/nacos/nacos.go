package conf

import (
	registry "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/knoci/roaming-world/pkg/nacosx"
)

// InitNacosConfig delegates remote Nacos config initialization to pkg/nacosx.
func InitNacosConfig() config.Config {
	return nacosx.InitNacosConfig()
}

// GetConfig returns the process-wide Nacos config.Config via pkg/nacosx.
func GetConfig() config.Config {
	return nacosx.GetConfig()
}

// GetConfigString reads a required string config value during startup via pkg/nacosx.
func GetConfigString(cfg config.Config, key string) string {
	return nacosx.MustGetString(cfg, key)
}

// GetConfigInt reads a required integer config value during startup via pkg/nacosx.
func GetConfigInt(cfg config.Config, key string) int {
	return nacosx.MustGetInt(cfg, key)
}

// NewRegistry creates a Nacos service registrar/discovery client via pkg/nacosx.
func NewRegistry() *registry.Registry {
	return nacosx.NewRegistry()
}
