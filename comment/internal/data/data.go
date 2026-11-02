package data

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	nacos "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/google/uuid"
	"github.com/google/wire"
	pd "github.com/knoci/roaming-world/comment/api/user/v1"
	"github.com/knoci/roaming-world/comment/internal/conf"
	nc "github.com/knoci/roaming-world/comment/internal/conf/nacos"
	"github.com/knoci/roaming-world/comment/internal/pkg"
	kafka "github.com/knoci/roaming-world/comment/internal/pkg"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewCommentRepo)

// Data 数据层结构体
type Data struct {
	db           *gorm.DB
	redis        *redis.Client
	event        *pkg.KafkaSender
	log          *log.Helper
	nacos        *nacos.Registry
	userClient   pd.UserClient
	eventTopic   string
	outboxCancel context.CancelFunc
	outboxWG     sync.WaitGroup
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
	err = db.AutoMigrate(&Comment{}, &Commentlike{}, &CommentOutbox{}, &CommentReport{})
	if err != nil {
		log.Errorf("commentData: failed to auto migrate: %v", err)
		return nil, nil, err
	}

	address := nc.GetConfigString(cfg, "kafka.address")
	eventTopic := nc.GetConfigString(cfg, "kafka.topic.comment-event")
	event, err := kafka.NewKafkaSyncSender([]string{address}, eventTopic)
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
		event:      event,
		eventTopic: eventTopic,
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
		if d.outboxCancel != nil {
			d.outboxCancel()
			d.outboxWG.Wait()
		}
		_ = d.event.Close()
	}

	d.startOutbox()

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

type CommentCountEvent struct {
	AID   string `json:"aid"`
	Count int64  `json:"count"`
}

// CommentOutbox 评论事件 outbox：先和评论写在同一事务，再由后台任务发 Kafka。
type CommentOutbox struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement"`
	EventID     string    `gorm:"type:varchar(36);not null;uniqueIndex:uk_comment_outbox_event_id"`
	Topic       string    `gorm:"type:varchar(100);not null"`
	EventKey    string    `gorm:"type:varchar(64);not null;index:idx_comment_outbox_key"`
	Payload     []byte    `gorm:"type:bytea;not null"`
	Status      string    `gorm:"type:varchar(20);not null;default:pending;index:idx_comment_outbox_pending,priority:1"`
	RetryCount  int       `gorm:"not null;default:0"`
	NextRetryAt time.Time `gorm:"not null;index:idx_comment_outbox_pending,priority:2"`
	CreatedAt   time.Time
	PublishedAt *time.Time
}

func (CommentOutbox) TableName() string { return "comment_outbox" }

// enqueueCommentCount 在评论事务内写入 outbox，保证“评论写入”和“计数事件”同生共死。
func (d *Data) enqueueCommentCount(tx *gorm.DB, aid string) error {
	var count int64
	if err := tx.Model(&Comment{}).Where("target = ?", aid).Count(&count).Error; err != nil {
		return err
	}
	body, err := json.Marshal(CommentCountEvent{AID: aid, Count: count})
	if err != nil {
		return err
	}
	return tx.Create(&CommentOutbox{
		EventID:     uuid.NewString(),
		Topic:       d.eventTopic,
		EventKey:    aid,
		Payload:     body,
		Status:      "pending",
		NextRetryAt: time.Now(),
	}).Error
}

func (d *Data) startOutbox() {
	ctx, cancel := context.WithCancel(context.Background())
	d.outboxCancel = cancel
	d.outboxWG.Add(1)
	go func() {
		defer d.outboxWG.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := d.publishOutbox(ctx); err != nil {
					d.log.WithContext(ctx).Errorf("commentData: publish outbox failed: %v", err)
				}
			}
		}
	}()
}

func (d *Data) publishOutbox(ctx context.Context) error {
	if d.event == nil || d.eventTopic == "" {
		return nil
	}
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var events []CommentOutbox
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND next_retry_at <= ?", "pending", time.Now()).
			Order("id").Limit(50).Find(&events).Error; err != nil {
			return err
		}
		for _, event := range events {
			if err := d.event.Send(ctx, kafka.NewMessage(event.EventKey, event.Payload)); err != nil {
				retry := event.RetryCount + 1
				delay := time.Duration(1<<minInt(retry, 6)) * time.Second
				if err := tx.Model(&CommentOutbox{}).Where("id = ?", event.ID).Updates(map[string]any{
					"retry_count":   retry,
					"next_retry_at": time.Now().Add(delay),
				}).Error; err != nil {
					return err
				}
				continue
			}
			now := time.Now()
			if err := tx.Model(&CommentOutbox{}).Where("id = ?", event.ID).Updates(map[string]any{
				"status":       "published",
				"retry_count":  0,
				"published_at": now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
