package data

import (
	"context"
	"encoding/json"
	"fmt"

	nacos "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/nacos-group/nacos-sdk-go/clients"
	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"github.com/nacos-group/nacos-sdk-go/vo"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/knoci/roaming-world/article/internal/conf"
	nc "github.com/knoci/roaming-world/article/internal/conf/nacos"
	kafka "github.com/knoci/roaming-world/article/internal/pkg"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewArticleRepo, NewNacosEngine)

// Data 数据层资源。
type Data struct {
	db        *gorm.DB
	redis     *redis.Client
	cdc       *kafka.KafkaSender
	logsender *kafka.KafkaSender
	log       *log.Helper
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

func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	l := log.NewHelper(logger)

	cfg := nc.GetConfig()
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s",
		nc.GetConfigString(cfg, "postgre.host"),
		nc.GetConfigString(cfg, "postgre.user"),
		nc.GetConfigString(cfg, "postgre.password"),
		nc.GetConfigString(cfg, "postgre.dbname"),
		nc.GetConfigInt(cfg, "postgre.port"),
		nc.GetConfigString(cfg, "postgre.sslmode"),
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		NamingStrategy:                           schema.NamingStrategy{},
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		l.Errorf("articleData: failed to connect database: %v", err)
		return nil, nil, err
	}

	if err := db.AutoMigrate(&Article{}, &Like{}, &Favorite{}); err != nil {
		l.Errorf("articleData: failed to auto migrate: %v", err)
		return nil, nil, err
	}

	address1 := nc.GetConfigString(cfg, "kafka.address.cdc")
	topic1 := nc.GetConfigString(cfg, "kafka.topic.cdc")
	cdc, err := kafka.NewKafkaSender([]string{address1}, topic1)
	if err != nil {
		l.Errorf("articleData: failed to create cdc sender: %v", err)
		return nil, nil, err
	}

	address2 := nc.GetConfigString(cfg, "kafka.address.log")
	topic2 := nc.GetConfigString(cfg, "kafka.topic.log")
	logsender, err := kafka.NewKafkaSender([]string{address2}, topic2)
	if err != nil {
		l.Errorf("articleData: failed to create log sender: %v", err)
		return nil, nil, err
	}

	redisHost := nc.GetConfigString(cfg, "redis.host")
	redisPort := nc.GetConfigInt(cfg, "redis.port")
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", redisHost, redisPort),
		Password: nc.GetConfigString(cfg, "redis.password"),
		DB:       nc.GetConfigInt(cfg, "redis.db"),
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		l.Errorf("articleData: failed to connect redis: %v", err)
		return nil, nil, err
	}

	d := &Data{
		db:        db,
		redis:     rdb,
		cdc:       cdc,
		logsender: logsender,
		log:       l,
	}

	cleanup := func() {
		l.Info("articleData: closing the data resources")
		if sqlDB, err := d.db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		_ = d.redis.Close()
		_ = d.cdc.Close()
		_ = d.logsender.Close()
	}

	return d, cleanup, nil
}

// NewNacosEngine 注册 article 服务到 Nacos，供 gateway 或其他服务发现。
func NewNacosEngine(c *conf.Data) *nacos.Registry {
	sc := []constant.ServerConfig{
		*constant.NewServerConfig(c.Nacos.Addr, c.Nacos.Port),
	}
	cc := constant.ClientConfig{
		NamespaceId:         c.Nacos.NamespaceId,
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		LogDir:              "tmp/nacos/log",
		CacheDir:            "tmp/nacos/cache",
		LogLevel:            "debug",
	}

	client, err := clients.NewNamingClient(
		vo.NacosClientParam{
			ClientConfig:  &cc,
			ServerConfigs: sc,
		},
	)
	if err != nil {
		panic(err)
	}
	return nacos.New(client)
}

func (d *Data) SendSqlLog(ctx context.Context, key string, sql string, params []interface{}) error {
	msg := SqlMsg{Query: sql, Params: params}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return d.cdc.Send(ctx, kafka.NewMessage(key, body))
}

func (d *Data) SendErrorLog(ctx context.Context, key string, errmsg string, errop string, errdata any) error {
	msg := ErrorMsg{ErrorMsg: errmsg, ErrorOp: errop, ErrorData: errdata}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return d.logsender.Send(ctx, kafka.NewMessage(key, body))
}
