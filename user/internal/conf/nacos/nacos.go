package conf

import (
	"time"

	registry "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/knoci/roaming-world/pkg/nacosx"
)

// InitNacosConfig initializes and loads a Kratos config.Config backed by Nacos.
func InitNacosConfig() config.Config {
	return nacosx.InitNacosConfig()
}

// GetConfig returns the process-wide Nacos config.Config.
func GetConfig() config.Config {
	return nacosx.GetConfig()
}

// GetConfigString reads a required string value from Nacos config.
func GetConfigString(cfg config.Config, key string) string {
	return nacosx.MustGetString(cfg, key)
}

// GetConfigInt reads a required int value from Nacos config.
func GetConfigInt(cfg config.Config, key string) int {
	return nacosx.MustGetInt(cfg, key)
}

// GetString reads a string value from cfg without terminating the process on error.
func GetString(cfg config.Config, key string) (string, error) {
	return nacosx.GetString(cfg, key)
}

// GetStringOrDefault reads a string value from cfg, returning def if missing or empty.
func GetStringOrDefault(cfg config.Config, key, def string) string {
	return nacosx.GetStringOrDefault(cfg, key, def)
}

// GetInt reads an integer value from cfg without terminating the process on error.
func GetInt(cfg config.Config, key string) (int, error) {
	return nacosx.GetInt(cfg, key)
}

// GetIntOrDefault reads an integer value from cfg, returning def if missing or invalid.
func GetIntOrDefault(cfg config.Config, key string, def int) int {
	return nacosx.GetIntOrDefault(cfg, key, def)
}

// GetDurationOrDefault reads a positive duration from cfg, returning def if missing or invalid.
func GetDurationOrDefault(cfg config.Config, key string, def time.Duration) time.Duration {
	return nacosx.GetDurationOrDefault(cfg, key, def)
}

// NewRegistry creates a Nacos service registrar/discovery client.
func NewRegistry() *registry.Registry {
	return nacosx.NewRegistry()
}
