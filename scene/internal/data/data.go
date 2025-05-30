package data

import (
	"context"
	"fmt"
	"github.com/knoci/roaming-world/scene/internal/conf"
	nacos "github.com/knoci/roaming-world/scene/internal/conf/nacos"
	kafka "github.com/knoci/roaming-world/scene/internal/pkg"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
	"github.com/meilisearch/meilisearch-go"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(
	NewData,
	NewSceneRepo,
)

// Data ..
type Data struct {
	db        *gorm.DB
	log       *log.Helper
	redis 	  *redis.Client
	cdc       *kafka.KafkaSender
	logsender *kafka.KafkaSender
	meili	  meilisearch.ServiceManager
}

type SqlMsg struct {
	Query  string        `json:"query"`
	Params []interface{} `json:"params"`
}

type ErrorMsg struct {
	ErrorMsg  string `json:"error_msg"`
	ErrorOp   string `json:"error_op"`
	ErrorData any    `json:"error_data"`
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
		log.Errorf("sceneData: failed to connect database: %v", err)
		return nil, nil, err
	}

	// 自动迁移表结构
	err = db.AutoMigrate(&Scene{})
	if err != nil {
		log.Errorf("sceneData: failed to auto migrate: %v", err)
		return nil, nil, err
	}


	address1 := nacos.GetConfigString(cfg, "kafka.address.cdc")
	topic1 := nacos.GetConfigString(cfg, "kafka.topic.cdc")
	cdc, err := kafka.NewKafkaSender([]string{address1}, topic1)
	if err != nil {
		panic(err)
	}

	address2 := nacos.GetConfigString(cfg, "kafka.address.log")
	topic2 := nacos.GetConfigString(cfg, "kafka.topic.log")
	logsender, err := kafka.NewKafkaSender([]string{address2}, topic2)
	if err != nil {
		panic(err)
	}

	meiliHost := nacos.GetConfigString(cfg, "meili.host")
	meiliKey := nacos.GetConfigString(cfg, "meili.key")
	meili := meilisearch.New(meiliHost, meilisearch.WithAPIKey(meiliKey))

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
		log.Error("foodData: failed to connect redis", err)
		return nil, nil, err
	}

	d := &Data{
		db:        db,
		log:       log,
		redis: 	   client,
		cdc:       cdc,
		logsender: logsender,
		meili: 	   meili,
	}

	cleanup := func() {
		log.Info("sceneData: closing the data resources")
		sqlDB, err := d.db.DB()
		if err != nil {
			log.Errorf("user Data: failed to get sqlDB: %v", err)
			return
		}
		sqlDB.Close()
	}

	return d, cleanup, nil
}
