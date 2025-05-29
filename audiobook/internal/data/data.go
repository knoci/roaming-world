package data

import (
	"context"
	
	"fmt"

	"github.com/knoci/roaming-world/audiobook/internal/conf"
	nacos "github.com/knoci/roaming-world/audiobook/conf/nacos"
	kafka "github.com/knoci/roaming-world/audiobook/internal/pkg"
	
	"github.com/redis/go-redis/v9"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"gorm.io/gorm"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewAudiobookRepo)

// Data .
type Data struct {
	db    *gorm.DB
	log   *log.Helper
	redis *redis.Client
	kafka *kafka.KafkaSender
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
		log.Errorf("failed to connect database: %v", err)
		return nil, nil, err
	}

	// 自动迁移表结构
	err = db.AutoMigrate(&Audiobook{}, &AudiobookDetail{})
	if err != nil {
		log.Errorf("failed to auto migrate: %v", err)
		return nil, nil, err
	}

	address := nacos.GetConfigString(cfg, "kafka.address")
	topic := nacos.GetConfigString(cfg, "kafka.topic")
	sender, err := kafka.NewKafkaSender([]string{address}, topic)
	if err != nil {
		panic(err)
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
		log.Error("failed to connect redis", err)
		return nil, nil, err
	}

	d := &Data{
		db:    db,
		log:   log,
		redis: client,
		kafka: sender,
	}

	cleanup := func() {
		log.Info("closing the data resources")
		sqlDB, err := d.db.DB()
		if err != nil {
			log.Errorf("failed to get sqlDB: %v", err)
			return
		}
		sqlDB.Close()
	}

	return d, cleanup, nil
}