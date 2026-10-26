package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nacos "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/google/wire"
	pd "github.com/knoci/roaming-world/comment/api/user/v1"
	"github.com/knoci/roaming-world/comment/internal/conf"
	nc "github.com/knoci/roaming-world/comment/internal/conf/nacos"
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
	db         *gorm.DB
	redis      *redis.Client
	cdc        *pkg.KafkaSender
	logsender  *pkg.KafkaSender
	log        *log.Helper
	nacos      *nacos.Registry
	userClient pd.UserClient
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

	nacosRegistry := nc.NewRegistry()

	conn, err := grpc.DialInsecure(context.Background(),
		grpc.WithEndpoint("discovery:///user.grpc"),
		grpc.WithDiscovery(nacosRegistry),
		grpc.WithTimeout(2*time.Second),
		grpc.WithMiddleware(recovery.Recovery()),
	)
	if err != nil {
		return nil, nil, err
	}
	userClient := pd.NewUserClient(conn)

	d := &Data{
		db:         db,
		log:        log,
		redis:      client,
		cdc:        cdc,
		logsender:  logsender,
		nacos:      nacosRegistry,
		userClient: userClient,
	}

	cleanup := func() {
		log.Info("commentData: closing the data resources")
		sqlDB, err := d.db.DB()
		if err != nil {
			log.Errorf("commentData: failed to get sqlDB: %v", err)
			return
		}
		sqlDB.Close()
		_ = conn.Close()
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