package nacosx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	knacos "github.com/go-kratos/kratos/contrib/config/nacos/v2"
	registry "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/nacos-group/nacos-sdk-go/clients"
	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"github.com/nacos-group/nacos-sdk-go/vo"
	"github.com/spf13/viper"
)

const (
	defaultNacosPort     uint64 = 8848
	defaultNacosDataID          = "roaming-world.yaml"
	defaultNacosGroup           = "DEFAULT_GROUP"
	defaultNacosLogLevel        = "warn"
	defaultNacosTimeout  uint64 = 5000
)

var defaultConfigPaths = []string{
	"../../configs/config.yaml",
	"./configs/config.yaml",
	"../configs/config.yaml",
	"/app/configs/config.yaml",
}

type connectionConfig struct {
	Addr        string
	Port        uint64
	NamespaceID string
	DataID      string
	GroupID     string
}

var (
	configOnce   sync.Once
	sharedConfig config.Config
)

// resolveConnectionConfig resolves Nacos server parameters from local config.yaml (if found)
// overlaid with NACOS_* environment variables. Pure and concurrency-safe (no mutable globals).
func resolveConnectionConfig() (connectionConfig, error) {
	var cfg connectionConfig

	paths := make([]string, 0, len(defaultConfigPaths)+1)
	if customPath := strings.TrimSpace(os.Getenv("NACOS_CONFIG_FILE")); customPath != "" {
		paths = append(paths, customPath)
	}
	paths = append(paths, defaultConfigPaths...)

	local := viper.New()
	foundFile := false
	for _, path := range paths {
		local.SetConfigFile(path)
		if err := local.ReadInConfig(); err == nil {
			cfg.Addr = strings.TrimSpace(local.GetString("data.nacos.addr"))
			cfg.Port = local.GetUint64("data.nacos.port")
			cfg.NamespaceID = strings.TrimSpace(local.GetString("data.nacos.namespaceId"))
			cfg.DataID = strings.TrimSpace(local.GetString("data.nacos.dataId"))
			cfg.GroupID = strings.TrimSpace(local.GetString("data.nacos.groupId"))
			foundFile = true
			break
		}
	}

	if v := strings.TrimSpace(os.Getenv("NACOS_ADDR")); v != "" {
		cfg.Addr = v
	}
	if v := strings.TrimSpace(os.Getenv("NACOS_PORT")); v != "" {
		if p, err := strconv.ParseUint(v, 10, 64); err == nil && p > 0 {
			cfg.Port = p
		}
	}
	if v, ok := os.LookupEnv("NACOS_NAMESPACE_ID"); ok {
		cfg.NamespaceID = strings.TrimSpace(v)
	}
	if v := strings.TrimSpace(os.Getenv("NACOS_DATA_ID")); v != "" {
		cfg.DataID = v
	}
	if v := strings.TrimSpace(os.Getenv("NACOS_GROUP_ID")); v != "" {
		cfg.GroupID = v
	}

	if cfg.Addr == "" {
		if !foundFile {
			return connectionConfig{}, errors.New("nacosx: config.yaml not found and NACOS_ADDR is empty")
		}
		return connectionConfig{}, errors.New("nacosx: data.nacos.addr and NACOS_ADDR are both empty")
	}
	if cfg.Port == 0 {
		cfg.Port = defaultNacosPort
	}
	if cfg.DataID == "" {
		cfg.DataID = defaultNacosDataID
	}
	if cfg.GroupID == "" {
		cfg.GroupID = defaultNacosGroup
	}

	return cfg, nil
}

func buildClientConfig(namespaceID string) constant.ClientConfig {
	logLevel := strings.TrimSpace(os.Getenv("NACOS_LOG_LEVEL"))
	if logLevel == "" {
		logLevel = defaultNacosLogLevel
	}
	logDir := strings.TrimSpace(os.Getenv("NACOS_LOG_DIR"))
	if logDir == "" {
		logDir = filepath.Join(os.TempDir(), "nacos", "log")
	}
	cacheDir := strings.TrimSpace(os.Getenv("NACOS_CACHE_DIR"))
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "nacos", "cache")
	}
	return constant.ClientConfig{
		NamespaceId:         namespaceID,
		TimeoutMs:           defaultNacosTimeout,
		NotLoadCacheAtStart: true,
		LogDir:              logDir,
		CacheDir:            cacheDir,
		LogLevel:            logLevel,
		Username:            os.Getenv("NACOS_USERNAME"),
		Password:            os.Getenv("NACOS_PASSWORD"),
	}
}

// InitNacosConfig initializes and loads a Kratos config.Config backed by Nacos.
// Intended for service startup; panics if Nacos configuration or connection fails.
func InitNacosConfig() config.Config {
	conn, err := resolveConnectionConfig()
	if err != nil {
		panic(err)
	}

	sc := []constant.ServerConfig{
		*constant.NewServerConfig(conn.Addr, conn.Port),
	}
	cc := buildClientConfig(conn.NamespaceID)

	client, err := clients.NewConfigClient(vo.NacosClientParam{
		ClientConfig:  &cc,
		ServerConfigs: sc,
	})
	if err != nil {
		panic(fmt.Errorf("nacosx: create config client: %w", err))
	}

	c := config.New(
		config.WithSource(
			knacos.NewConfigSource(
				client,
				knacos.WithGroup(conn.GroupID),
				knacos.WithDataID(conn.DataID),
			),
		),
	)
	if err := c.Load(); err != nil {
		panic(fmt.Errorf("nacosx: load remote config: %w", err))
	}
	return c
}

// GetConfig returns the process-wide Nacos config.Config, initialized safely once via sync.Once.
func GetConfig() config.Config {
	configOnce.Do(func() {
		log.Info("nacosx: initializing remote config")
		sharedConfig = InitNacosConfig()
	})
	return sharedConfig
}

// NewRegistry creates a Kratos Nacos service registrar/discovery client using resolved Nacos settings.
// Intended for service startup; panics if Nacos configuration or naming client creation fails.
func NewRegistry() *registry.Registry {
	conn, err := resolveConnectionConfig()
	if err != nil {
		panic(err)
	}

	sc := []constant.ServerConfig{
		*constant.NewServerConfig(conn.Addr, conn.Port),
	}
	cc := buildClientConfig(conn.NamespaceID)

	client, err := clients.NewNamingClient(vo.NacosClientParam{
		ClientConfig:  &cc,
		ServerConfigs: sc,
	})
	if err != nil {
		panic(fmt.Errorf("nacosx: create naming client: %w", err))
	}
	return registry.New(client)
}

// GetString reads a string value from cfg without terminating the process on error.
func GetString(cfg config.Config, key string) (string, error) {
	if cfg == nil {
		return "", errors.New("nacosx: config is nil")
	}
	val, err := cfg.Value(key).String()
	if err != nil {
		return "", fmt.Errorf("nacosx: get string %q: %w", key, err)
	}
	return val, nil
}

// GetStringOrDefault reads a string value from cfg, returning def if missing, empty, or invalid.
func GetStringOrDefault(cfg config.Config, key, def string) string {
	val, err := GetString(cfg, key)
	if err != nil || strings.TrimSpace(val) == "" {
		return def
	}
	return val
}

// GetInt reads an integer value from cfg without terminating the process on error.
func GetInt(cfg config.Config, key string) (int, error) {
	if cfg == nil {
		return 0, errors.New("nacosx: config is nil")
	}
	val, err := cfg.Value(key).Int()
	if err != nil {
		return 0, fmt.Errorf("nacosx: get int %q: %w", key, err)
	}
	return int(val), nil
}

// GetIntOrDefault reads an integer value from cfg, returning def if missing or invalid.
func GetIntOrDefault(cfg config.Config, key string, def int) int {
	val, err := GetInt(cfg, key)
	if err != nil {
		return def
	}
	return val
}

// GetDurationOrDefault reads a positive time.Duration from cfg.
// It supports Go duration strings (e.g. "30m", "5s") as well as integer nanosecond values.
// Returns def if cfg is nil, the key is missing/invalid, or the parsed duration is <= 0.
func GetDurationOrDefault(cfg config.Config, key string, def time.Duration) time.Duration {
	if cfg == nil {
		return def
	}
	if s, err := cfg.Value(key).String(); err == nil {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			if d, parseErr := time.ParseDuration(trimmed); parseErr == nil {
				if d > 0 {
					return d
				}
				return def
			}
		}
	}
	if d, err := cfg.Value(key).Duration(); err == nil && d > 0 {
		return d
	}
	return def
}

// MustGetString reads a required string config value and panics if unavailable.
// ONLY use during startup config loading; use GetString or GetStringOrDefault in request paths.
func MustGetString(cfg config.Config, key string) string {
	val, err := GetString(cfg, key)
	if err != nil {
		panic(err)
	}
	return val
}

// MustGetInt reads a required integer config value and panics if unavailable.
// ONLY use during startup config loading; use GetInt or GetIntOrDefault in request paths.
func MustGetInt(cfg config.Config, key string) int {
	val, err := GetInt(cfg, key)
	if err != nil {
		panic(err)
	}
	return val
}
