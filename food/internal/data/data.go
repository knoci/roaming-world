package data

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/food/internal/conf"
	nacos "github.com/knoci/roaming-world/food/internal/conf/nacos"
	kafka "github.com/knoci/roaming-world/food/internal/pkg"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewFoodRepo)

// Data .
type Data struct {
	db        *gorm.DB
	redis     *redis.Client
	log       *log.Helper
	cdc       *kafka.KafkaSender
	logsender *kafka.KafkaSender
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
		log.Errorf("foodData: failed to connect database: %v", err)
		return nil, nil, err
	}

	// 自动迁移表结构
	err = db.AutoMigrate(&Food{})
	if err != nil {
		log.Errorf("foodData: failed to auto migrate: %v", err)
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
		redis:     client,
		log:       log,
		cdc:       cdc,
		logsender: logsender,
	}

	cleanup := func() {
		log.Info("foodData: closing the data resources")
		sqlDB, err := d.db.DB()
		if err != nil {
			log.Errorf("foodData: failed to get sqlDB: %v", err)
			return
		}
		sqlDB.Close()
	}

	return d, cleanup, nil
}

func (d *Data) SendSqlLog(ctx context.Context, key string, sql string, params []interface{}) error {
	msg := SqlMsg{
		Query:  sql,
		Params: params,
	}
	sqlbyte, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	sqllog := kafka.NewMessage(key, sqlbyte)
	err = d.cdc.Send(ctx, sqllog)
	if err != nil {
		return err
	}
	return nil
}

func (d *Data) SendErrorLog(ctx context.Context, key string, errmsg string, errop string, errdata any) error {
	msg := ErrorMsg{
		ErrorMsg:  errmsg,
		ErrorOp:   errop,
		ErrorData: errdata,
	}
	errbyte, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	errorlog := kafka.NewMessage(key, errbyte)
	err = d.logsender.Send(ctx, errorlog)
	if err != nil {
		return err
	}
	return nil
}
