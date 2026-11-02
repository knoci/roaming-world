package data

import (
	"context"
	"fmt"

	"github.com/knoci/roaming-world/audiobook/internal/conf"
	nacos "github.com/knoci/roaming-world/audiobook/internal/conf/nacos"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewAudiobookRepo)

// Data .
type Data struct {
	db    *gorm.DB
	log   *log.Helper
	redis *redis.Client
}

// NewData .
func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	log := log.NewHelper(logger)

	cfg := nacos.GetConfig()
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s",
		nacos.GetConfigString(cfg, "postgre.host"),
		nacos.GetConfigString(cfg, "postgre.user"),
		nacos.GetConfigString(cfg, "postgre.password"),
		nacos.GetConfigString(cfg, "postgre.dbname"),
		nacos.GetConfigInt(cfg, "postgre.port"),
		nacos.GetConfigString(cfg, "postgre.sslmode"),
	)

	// 连接数据库
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			NameReplacer: nil,
		},
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		log.Errorf("audiobookData: failed to connect database: %v", err)
		return nil, nil, err
	}

	// 自动迁移表结构
	err = db.AutoMigrate(&Audiobook{}, &AudiobookDetail{})
	if err != nil {
		log.Errorf("audiobookData: failed to auto migrate: %v", err)
		return nil, nil, err
	}

	redisHost := nacos.GetConfigString(cfg, "redis.host")
	redisPort := nacos.GetConfigInt(cfg, "redis.port")
	redisAddr := fmt.Sprintf("%s:%d", redisHost, redisPort)
	client := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: nacos.GetConfigString(cfg, "redis.password"),
		DB:       nacos.GetConfigInt(cfg, "redis.db"),
	})
	// 测试连接
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		log.Error("audiobookData: failed to connect redis", err)
		return nil, nil, err
	}

	d := &Data{
		db:    db,
		log:   log,
		redis: client,
	}

	cleanup := func() {
		log.Info("audiobookData: closing the data resources")
		sqlDB, err := d.db.DB()
		if err != nil {
			log.Errorf("audiobookData: failed to get sqlDB: %v", err)
			return
		}
		sqlDB.Close()
	}

	return d, cleanup, nil
}

func (d *Data) LogSQLAudit(ctx context.Context, key string, sql string, params []interface{}) error {
	d.log.WithContext(ctx).Debugf("sql audit: %s", sql)
	return nil
}

func (d *Data) LogError(ctx context.Context, key string, errmsg string, errop string, errdata any) error {
	d.log.WithContext(ctx).Errorf("module=%s op=%s error=%s data=%v", key, errop, errmsg, errdata)
	return nil
}
