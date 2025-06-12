package data

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-kratos/kratos/v2/log"
	nacos "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/comment/internal/conf"
	"github.com/spf13/viper"
	nc "github.com/knoci/roaming-world/comment/internal/conf/nacos"
	"github.com/nacos-group/nacos-sdk-go/clients"
	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"github.com/nacos-group/nacos-sdk-go/vo"
	"github.com/knoci/roaming-world/comment/internal/pkg"
	kafka "github.com/knoci/roaming-world/comment/internal/pkg"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewCommentRepo)

// Data 数据层结构体
type Data struct {
	db        *gorm.DB
	redis     *redis.Client
	cdc       *pkg.KafkaSender
	logsender *pkg.KafkaSender
	log       *log.Helper
	nacos     *nacos.Registry
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

// NewData 初始化数据层资源
func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	log := log.NewHelper(logger)

	cfg := nc.GetConfig()
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s",
		nc.GetConfigString(cfg, "postgre.host"),
		nc.GetConfigString(cfg, "postgre.user"),
		nc.GetConfigString(cfg, "postgre.password"),
		nc.GetConfigString(cfg, "postgre.dbname"),
		nc.GetConfigInt(cfg, "postgre.port"),
		nc.GetConfigString(cfg, "postgre.sslmode"),
	)

	// 连接数据库
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			NameReplacer: nil,
		},
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		log.Errorf("commentData: failed to connect database: %v", err)
		return nil, nil, err
	}

	// 自动迁移表结构
	err = db.AutoMigrate(&Comment{}, &Commentlike{})
	if err != nil {
		log.Errorf("commentData: failed to auto migrate: %v", err)
		return nil, nil, err
	}

	address1 := nc.GetConfigString(cfg, "kafka.address.cdc")
	topic1 := nc.GetConfigString(cfg, "kafka.topic.cdc")
	cdc, err := kafka.NewKafkaSender([]string{address1}, topic1)
	if err != nil {
		panic(err)
	}

	address2 := nc.GetConfigString(cfg, "kafka.address.log")
	topic2 := nc.GetConfigString(cfg, "kafka.topic.log")
	logsender, err := kafka.NewKafkaSender([]string{address2}, topic2)
	if err != nil {
		panic(err)
	}

	redisHost := nc.GetConfigString(cfg, "redis.host")
	redisPort := nc.GetConfigInt(cfg, "redis.port")
	redisAddr := fmt.Sprintf("%s:%d", redisHost, redisPort)
	client := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: nc.GetConfigString(cfg, "redis.password"),
		DB:       nc.GetConfigInt(cfg, "redis.db"),
	})
	// 测试连接
	ctx := context.Background()
	if err = client.Ping(ctx).Err(); err != nil {
		log.Error("commentData: failed to connect redis", err)
		return nil, nil, err
	}

	localConfig := viper.New()                                
	localConfig.SetConfigFile("..\\..\\configs\\config.yaml") 
	if err := localConfig.ReadInConfig(); err != nil {
		panic(err)
	}
	nacosIp := localConfig.GetString("data.nacos.addr")
	nacosPort := localConfig.GetUint64("data.nacos.port")
	nacosNameSpaceId := localConfig.GetString("data.nacos.namespaceId")
	sc := []constant.ServerConfig{
		*constant.NewServerConfig(nacosIp, nacosPort),
	}

	cc := constant.ClientConfig{
		NamespaceId:         nacosNameSpaceId,
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		LogDir:              "tmp/nacos/log",
		CacheDir:            "tmp/nacos/cache",
		LogLevel:            "debug",
	}

	clientn, err := clients.NewNamingClient(
		vo.NacosClientParam{
			ClientConfig:  &cc,
			ServerConfigs: sc,
		},
	)

	if err != nil {
		panic(err)
	}

	nacos := nacos.New(clientn)

	d := &Data{
		db:        db,
		log:       log,
		redis:     client,
		cdc:       cdc,
		logsender: logsender,
		nacos:     nacos,
	}

	cleanup := func() {
		log.Info("commentData: closing the data resources")
		sqlDB, err := d.db.DB()
		if err != nil {
			log.Errorf("commentData: failed to get sqlDB: %v", err)
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
