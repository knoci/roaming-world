package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	nacos "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/google/wire"
	userv1 "github.com/knoci/roaming-world/article/api/user/v1"
	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	commentConsumer *kafka.KafkaReceiver
	userClient      userv1.UserClient
	userConn        *grpc.ClientConn
	log             *log.Helper
}

type CommentCountEvent struct {
	AID string `json:"aid"`
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
		consumer.Receive(context.Background(), d.handleCommentCountEvent)
	}

	cleanup := func() {
		l.Info("articleData: closing the data resources")
		if d.commentConsumer != nil {
			_ = d.commentConsumer.Close()
		}
		if sqlDB, err := d.db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		if d.userConn != nil {
			_ = d.userConn.Close()
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

func (d *Data) handleCommentCountEvent(ctx context.Context, payload []byte) error {
	var event CommentCountEvent
	if err := json.Unmarshal(payload, &event); err != nil || event.AID == "" {
		d.log.WithContext(ctx).Warn("articleData: discarding invalid comment count event")
		return nil
	}
	return d.refreshCommentCount(ctx, event.AID)
}

func (d *Data) refreshCommentCount(ctx context.Context, aid string) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var article Article
		err := tx.Clauses(clause.Locking{Strength: "NO KEY UPDATE"}).
			Select("aid").Where("aid = ?", aid).Take(&article).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// Take the count in a new statement after obtaining the article lock.
		count := tx.Table("comments").Select("COUNT(*)").Where("target = ? AND status = ?", aid, "active")
		return tx.Model(&article).UpdateColumn("comments", count).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}
