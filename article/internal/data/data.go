package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nacos "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/google/wire"
	userv1 "github.com/knoci/roaming-world/article/api/user/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
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
	db              *gorm.DB
	redis           *redis.Client
	commentConsumer *kafka.KafkaReceiver
	userClient      userv1.UserClient
	userConn        *grpc.ClientConn
	log             *log.Helper
}

type CommentCountEvent struct {
	AID   string `json:"aid"`
	Count int64  `json:"count"`
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

	address := nc.GetConfigString(cfg, "kafka.address")

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

	registry := nc.NewRegistry()
	conn, err := kratosgrpc.DialInsecure(context.Background(),
		kratosgrpc.WithEndpoint("discovery:///user.grpc"),
		kratosgrpc.WithDiscovery(registry),
		kratosgrpc.WithTimeout(2*time.Second),
		kratosgrpc.WithMiddleware(recovery.Recovery()),
	)
	if err != nil {
		l.Errorf("articleData: failed to connect user grpc: %v", err)
		return nil, nil, err
	}

	d := &Data{
		db:         db,
		redis:      rdb,
		userClient: userv1.NewUserClient(conn),
		userConn:   conn,
		log:        l,
	}

	eventTopic := nc.GetConfigString(cfg, "kafka.topic.comment-event")
	if eventTopic != "" {
		consumer, err := kafka.NewKafkaReceiver([]string{address}, eventTopic, "article-comment-consumer")
		if err != nil {
			l.Errorf("articleData: failed to create comment consumer: %v", err)
			return nil, nil, err
		}
		d.commentConsumer = consumer
		if err := consumer.Receive(context.Background(), d.handleCommentCountEvent); err != nil {
			l.Errorf("articleData: failed to start comment consumer: %v", err)
			return nil, nil, err
		}
	}

	cleanup := func() {
		l.Info("articleData: closing the data resources")
		if sqlDB, err := d.db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		_ = d.redis.Close()
		if d.userConn != nil {
			_ = d.userConn.Close()
		}
		if d.commentConsumer != nil {
			_ = d.commentConsumer.Close()
		}
	}

	return d, cleanup, nil
}

// NewNacosEngine 注册 article 服务到 Nacos，供 gateway 或其他服务发现。
func NewNacosEngine(c *conf.Data) *nacos.Registry {
	return nc.NewRegistry()
}

func (d *Data) LogSQLAudit(ctx context.Context, key string, sql string, params []interface{}) error {
	d.log.WithContext(ctx).Debugf("sql audit: %s", sql)
	return nil
}

func (d *Data) LogError(ctx context.Context, key string, errmsg string, errop string, errdata any) error {
	d.log.WithContext(ctx).Errorf("module=%s op=%s error=%s data=%v", key, errop, errmsg, errdata)
	return nil
}

func (d *Data) handleCommentCountEvent(ctx context.Context, msg *kafka.Message) error {
	var event CommentCountEvent
	if err := json.Unmarshal(msg.Value(), &event); err != nil {
		return err
	}
	if event.AID == "" {
		return nil
	}
	if err := d.db.WithContext(ctx).Model(&Article{}).Where("aid = ?", event.AID).UpdateColumn("comments", event.Count).Error; err != nil {
		return err
	}
	if err := d.redis.ZAdd(ctx, keyArticleComments, redis.Z{Score: float64(event.Count), Member: event.AID}).Err(); err != nil {
		d.log.WithContext(ctx).Errorf("articleData: update comment zset failed: %v", err)
	}
	return nil
}
