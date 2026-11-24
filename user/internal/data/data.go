package data

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	nacos "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/pkg/auth"
	"github.com/knoci/roaming-world/pkg/dbx"
	"github.com/knoci/roaming-world/pkg/health"
	"github.com/knoci/roaming-world/user/internal/biz"
	"github.com/knoci/roaming-world/user/internal/conf"
	nc "github.com/knoci/roaming-world/user/internal/conf/nacos"
	"github.com/redis/go-redis/v9"
	"github.com/tencentyun/cos-go-sdk-v5"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const (
	defaultRedisDialTimeout  = 5 * time.Second
	defaultRedisReadTimeout  = 3 * time.Second
	defaultRedisWriteTimeout = 3 * time.Second
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(
	NewData,
	NewUserRepo,
	NewSocialRepo,
	ProvideSessionStore,
	ProvideReadinessChecks,
	NewNacosEngine,
)

// Data holds database, Redis, session store, and object storage clients.
type Data struct {
	db       *gorm.DB
	redis    *redis.Client
	sessions *auth.RedisSessionStore
	log      *log.Helper
	cos      *cos.Client
}

// NewData initializes PostgreSQL (with pooling), Redis (with Ping), and COS clients.
func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	helper := log.NewHelper(logger)

	cfg := nc.GetConfig()
	dsn := dbx.BuildPostgresDSN(
		nc.GetConfigString(cfg, "postgre.host"),
		nc.GetConfigString(cfg, "postgre.user"),
		nc.GetConfigString(cfg, "postgre.password"),
		nc.GetConfigString(cfg, "postgre.dbname"),
		nc.GetConfigInt(cfg, "postgre.port"),
		nc.GetStringOrDefault(cfg, "postgre.sslmode", "disable"),
	)

	db, err := dbx.OpenPostgres(dsn, &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			NameReplacer: nil,
		},
		DisableForeignKeyConstraintWhenMigrating: true,
	}, dbx.DefaultPoolConfig())
	if err != nil {
		helper.Errorf("userData: failed to connect database: %v", err)
		return nil, nil, err
	}

	closeDB := func() {
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}

	if dbx.ShouldAutoMigrate() {
		if err := db.AutoMigrate(&User{}, &Follow{}, &Notification{}); err != nil {
			closeDB()
			helper.Errorf("userData: failed to auto migrate: %v", err)
			return nil, nil, err
		}
	}

	redisHost := nc.GetConfigString(cfg, "redis.host")
	redisPort := nc.GetConfigInt(cfg, "redis.port")
	redisPassword := nc.GetStringOrDefault(cfg, "redis.password", "")
	redisDB := nc.GetIntOrDefault(cfg, "redis.db", 0)

	rdb := redis.NewClient(&redis.Options{
		Addr:         net.JoinHostPort(redisHost, strconv.Itoa(redisPort)),
		Password:     redisPassword,
		DB:           redisDB,
		DialTimeout:  nc.GetDurationOrDefault(cfg, "redis.dial_timeout", defaultRedisDialTimeout),
		ReadTimeout:  nc.GetDurationOrDefault(cfg, "redis.read_timeout", defaultRedisReadTimeout),
		WriteTimeout: nc.GetDurationOrDefault(cfg, "redis.write_timeout", defaultRedisWriteTimeout),
	})

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = rdb.Ping(pingCtx).Err()
	cancel()
	if err != nil {
		_ = rdb.Close()
		closeDB()
		helper.Errorf("userData: failed to connect redis: %v", err)
		return nil, nil, err
	}

	bucketURL := nc.GetConfigString(cfg, "cos.bucket_url")
	secretID := nc.GetConfigString(cfg, "cos.secret_id")
	secretKey := nc.GetConfigString(cfg, "cos.secret_key")

	if bucketURL == "" || secretID == "" || secretKey == "" {
		_ = rdb.Close()
		closeDB()
		helper.Error("userData: cos config missing")
		return nil, nil, fmt.Errorf("userData: cos config missing")
	}

	u, err := url.Parse(bucketURL)
	if err != nil {
		_ = rdb.Close()
		closeDB()
		helper.Errorf("userData: parse cos Bucket URL failed: %v", err)
		return nil, nil, err
	}

	b := &cos.BaseURL{BucketURL: u}
	cosClient := cos.NewClient(b, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretID,
			SecretKey: secretKey,
		},
	})

	d := &Data{
		db:       db,
		redis:    rdb,
		sessions: auth.NewRedisSessionStore(rdb),
		log:      helper,
		cos:      cosClient,
	}

	cleanup := func() {
		helper.Info("userData: closing the data resources")
		if d.redis != nil {
			if err := d.redis.Close(); err != nil {
				helper.Errorf("userData: failed to close redis: %v", err)
			}
		}
		if d.db != nil {
			sqlDB, err := d.db.DB()
			if err != nil {
				helper.Errorf("userData: failed to get sqlDB: %v", err)
				return
			}
			if err := sqlDB.Close(); err != nil {
				helper.Errorf("userData: failed to close sqlDB: %v", err)
			}
		}
	}

	return d, cleanup, nil
}

// SessionStore returns the Redis-backed session and token revocation store.
func (d *Data) SessionStore() *auth.RedisSessionStore {
	if d == nil {
		return nil
	}
	if d.sessions != nil {
		return d.sessions
	}
	if d.redis != nil {
		return auth.NewRedisSessionStore(d.redis)
	}
	return nil
}

// ReadinessChecks returns dependency readiness probes for PostgreSQL and Redis.
func (d *Data) ReadinessChecks() []health.Check {
	return []health.Check{
		{
			Name: "postgres",
			Fn: func(ctx context.Context) error {
				if d == nil || d.db == nil {
					return errors.New("postgres not initialized")
				}
				sqlDB, err := d.db.DB()
				if err != nil {
					return err
				}
				return sqlDB.PingContext(ctx)
			},
		},
		{
			Name: "redis",
			Fn: func(ctx context.Context) error {
				if d == nil || d.redis == nil {
					return errors.New("redis not initialized")
				}
				return d.redis.Ping(ctx).Err()
			},
		},
	}
}

// ProvideSessionStore exposes Data.SessionStore() as biz.SessionStore for Wire.
func ProvideSessionStore(d *Data) biz.SessionStore {
	store := d.SessionStore()
	if store == nil {
		return nil
	}
	return store
}

// ProvideReadinessChecks exposes Data.ReadinessChecks() for Wire.
func ProvideReadinessChecks(d *Data) []health.Check {
	return d.ReadinessChecks()
}

func (d *Data) LogSQLAudit(ctx context.Context, key string, sql string, params []interface{}) error {
	if d == nil || d.log == nil {
		return nil
	}
	d.log.WithContext(ctx).Debugf("sql audit: %s", sql)
	return nil
}

func (d *Data) LogError(ctx context.Context, key string, errmsg string, errop string, errdata any) error {
	if d == nil || d.log == nil {
		return nil
	}
	d.log.WithContext(ctx).Errorf("module=%s op=%s error=%s data=%v", key, errop, errmsg, errdata)
	return nil
}

func NewNacosEngine(c *conf.Data) *nacos.Registry {
	return nc.NewRegistry()
}
