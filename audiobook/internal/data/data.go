package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/audiobook/internal/conf"
	nacos "github.com/knoci/roaming-world/audiobook/internal/conf/nacos"
	"github.com/knoci/roaming-world/pkg/dbx"
	"github.com/knoci/roaming-world/pkg/health"
	"github.com/knoci/roaming-world/pkg/nacosx"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(
	NewData,
	NewHealthChecks,
	NewAudiobookRepo,
)

// Data .
type Data struct {
	db    *gorm.DB
	log   *log.Helper
	redis *redis.Client
}

// NewData .
func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	helper := log.NewHelper(logger)

	cfg := nacos.GetConfig()
	dsn := dbx.BuildPostgresDSN(
		nacos.GetConfigString(cfg, "postgre.host"),
		nacos.GetConfigString(cfg, "postgre.user"),
		nacos.GetConfigString(cfg, "postgre.password"),
		nacos.GetConfigString(cfg, "postgre.dbname"),
		nacos.GetConfigInt(cfg, "postgre.port"),
		nacosx.GetStringOrDefault(cfg, "postgre.sslmode", "disable"),
	)

	// 连接数据库并配置连接池
	db, err := dbx.OpenPostgres(dsn, &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			NameReplacer: nil,
		},
		DisableForeignKeyConstraintWhenMigrating: true,
	}, dbx.DefaultPoolConfig())
	if err != nil {
		helper.Errorf("audiobookData: failed to connect database: %v", err)
		return nil, nil, err
	}

	if dbx.ShouldAutoMigrate() {
		if err = db.AutoMigrate(&Audiobook{}, &AudiobookDetail{}); err != nil {
			helper.Errorf("audiobookData: failed to auto migrate: %v", err)
			return nil, nil, err
		}
	}

	redisHost := nacos.GetConfigString(cfg, "redis.host")
	redisPort := nacos.GetConfigInt(cfg, "redis.port")
	redisPassword := nacosx.GetStringOrDefault(cfg, "redis.password", "")
	redisDB := nacosx.GetIntOrDefault(cfg, "redis.db", 0)

	client := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", redisHost, redisPort),
		Password:     redisPassword,
		DB:           redisDB,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelPing()
	if err := client.Ping(pingCtx).Err(); err != nil {
		helper.Errorf("audiobookData: failed to connect redis: %v", err)
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		_ = client.Close()
		return nil, nil, err
	}

	d := &Data{
		db:    db,
		log:   helper,
		redis: client,
	}

	cleanup := func() {
		helper.Info("audiobookData: closing the data resources")
		if d.redis != nil {
			if err := d.redis.Close(); err != nil {
				helper.Errorf("audiobookData: failed to close redis: %v", err)
			}
		}
		if d.db != nil {
			sqlDB, err := d.db.DB()
			if err != nil {
				helper.Errorf("audiobookData: failed to get sqlDB: %v", err)
				return
			}
			if err := sqlDB.Close(); err != nil {
				helper.Errorf("audiobookData: failed to close sqlDB: %v", err)
			}
		}
	}

	return d, cleanup, nil
}

// NewHealthChecks 返回有声书服务就绪探针所需的依赖检查项。
func NewHealthChecks(d *Data) []health.Check {
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
