package data

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/knoci/roaming-world/pkg/dbx"
	"github.com/knoci/roaming-world/pkg/health"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewCommentRepo)

const (
	outboxBatchSize         = 50
	outboxMaxBatchesPerTick = 20
	outboxLeaseDuration     = 60 * time.Second
	outboxPublishTimeout    = 5 * time.Second
	outboxMaxRetries        = 10
	outboxMaxBackoffSec     = 64

	outboxStatusPending    = "pending"
	outboxStatusProcessing = "processing"
	outboxStatusPublished  = "published"
	outboxStatusDeadLetter = "dead_letter"
)

type outboxOutcome struct {
	ID          uint64
	Published   bool
	Status      string
	RetryCount  int
	NextRetryAt time.Time
	PublishedAt time.Time
}

type outboxStore interface {
	Claim(ctx context.Context, limit int, lease time.Duration) ([]CommentOutbox, error)
	ApplyOutcomes(ctx context.Context, outcomes []outboxOutcome) error
}

type eventPublisher interface {
	Send(ctx context.Context, message *pkg.Message) error
	Close() error
}

type gormOutboxStore struct {
	db *gorm.DB
}

func (s *gormOutboxStore) Claim(ctx context.Context, limit int, lease time.Duration) ([]CommentOutbox, error) {
	var events []CommentOutbox
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status IN ('pending', 'processing') AND next_retry_at <= ?", now).
			Order("id").
			Limit(limit).
			Find(&events).Error; err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}
		ids := make([]uint64, len(events))
		for i := range events {
			ids[i] = events[i].ID
		}
		return tx.Model(&CommentOutbox{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":        outboxStatusProcessing,
				"next_retry_at": now.Add(lease),
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (s *gormOutboxStore) ApplyOutcomes(ctx context.Context, outcomes []outboxOutcome) error {
	if len(outcomes) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		publishedIDs := make([]uint64, 0, len(outcomes))
		var publishedAt time.Time
		for _, out := range outcomes {
			if out.Published {
				publishedIDs = append(publishedIDs, out.ID)
				if publishedAt.IsZero() {
					publishedAt = out.PublishedAt
				}
				continue
			}
			if out.Status == outboxStatusDeadLetter {
				if err := tx.Model(&CommentOutbox{}).Where("id = ?", out.ID).Updates(map[string]any{
					"status":      outboxStatusDeadLetter,
					"retry_count": out.RetryCount,
				}).Error; err != nil {
					return err
				}
				continue
			}
			if err := tx.Model(&CommentOutbox{}).Where("id = ?", out.ID).Updates(map[string]any{
				"status":        outboxStatusPending,
				"retry_count":   out.RetryCount,
				"next_retry_at": out.NextRetryAt,
			}).Error; err != nil {
				return err
			}
		}
		if len(publishedIDs) > 0 {
			if publishedAt.IsZero() {
				publishedAt = time.Now()
			}
			if err := tx.Model(&CommentOutbox{}).Where("id IN ?", publishedIDs).Updates(map[string]any{
				"status":       outboxStatusPublished,
				"published_at": publishedAt,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Data 数据层结构体
type Data struct {
	db             *gorm.DB
	event          eventPublisher
	outboxStore    outboxStore
	log            *log.Helper
	nacos          *nacos.Registry
	userClient     pd.UserClient
	eventTopic     string
	outboxCancel   context.CancelFunc
	outboxWG       sync.WaitGroup
	outboxInterval time.Duration
}

// NewData 初始化数据层资源
func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	helper := log.NewHelper(logger)

	cfg := nc.GetConfig()
	dsn := dbx.BuildPostgresDSN(
		nc.GetConfigString(cfg, "postgre.host"),
		nc.GetConfigString(cfg, "postgre.user"),
		nc.GetConfigString(cfg, "postgre.password"),
		nc.GetConfigString(cfg, "postgre.dbname"),
		nc.GetConfigInt(cfg, "postgre.port"),
		nc.GetConfigString(cfg, "postgre.sslmode"),
	)

	// 连接数据库
	db, err := dbx.OpenPostgres(dsn, &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			NameReplacer: nil,
		},
		DisableForeignKeyConstraintWhenMigrating: true,
	}, dbx.DefaultPoolConfig())
	if err != nil {
		helper.Errorf("commentData: failed to connect database: %v", err)
		return nil, nil, err
	}

	closeDB := func() error {
		sqlDB, dbErr := db.DB()
		if dbErr != nil {
			helper.Errorf("commentData: failed to get sqlDB: %v", dbErr)
			return dbErr
		}
		return sqlDB.Close()
	}

	// 按需自动迁移表结构（默认关闭，需显式设置 ROAMING_AUTO_MIGRATE=true）
	if dbx.ShouldAutoMigrate() {
		if err = db.AutoMigrate(&Comment{}, &Commentlike{}, &CommentOutbox{}, &CommentReport{}); err != nil {
			helper.Errorf("commentData: failed to auto migrate: %v", err)
			_ = closeDB()
			return nil, nil, err
		}
	}

	address := nc.GetConfigString(cfg, "kafka.address")
	eventTopic := nc.GetConfigString(cfg, "kafka.topic.comment-event")
	event, err := pkg.NewKafkaSyncSender(pkg.ParseBrokers(address), eventTopic)
	if err != nil {
		helper.Errorf("commentData: failed to create kafka sender: %v", err)
		_ = closeDB()
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
		_ = event.Close()
		_ = closeDB()
		return nil, nil, err
	}
	userClient := pd.NewUserClient(conn)

	d := &Data{
		db:          db,
		log:         helper,
		event:       event,
		outboxStore: &gormOutboxStore{db: db},
		eventTopic:  eventTopic,
		nacos:       nacosRegistry,
		userClient:  userClient,
	}

	cleanup := func() {
		helper.Info("commentData: closing the data resources")
		d.shutdown(conn.Close, closeDB)
	}

	d.startOutbox()

	return d, cleanup, nil
}

// shutdown enforces graceful teardown order:
// 1. Stop outbox background worker and wait for in-flight cycle to finish
// 2. Close Kafka writer
// 3. Close gRPC connection
// 4. Close PostgreSQL connection pool
func (d *Data) shutdown(closeGRPC func() error, closeDB func() error) {
	if d.outboxCancel != nil {
		d.outboxCancel()
		d.outboxWG.Wait()
	}
	if d.event != nil {
		if err := d.event.Close(); err != nil && d.log != nil {
			d.log.Errorf("commentData: failed to close kafka writer: %v", err)
		}
	}
	if closeGRPC != nil {
		if err := closeGRPC(); err != nil && d.log != nil {
			d.log.Errorf("commentData: failed to close grpc conn: %v", err)
		}
	}
	if closeDB != nil {
		if err := closeDB(); err != nil && d.log != nil {
			d.log.Errorf("commentData: failed to close sqlDB: %v", err)
		}
	}
}

// PingDB verifies PostgreSQL connectivity for readiness probes.
func (d *Data) PingDB(ctx context.Context) error {
	if d == nil || d.db == nil {
		return errors.New("commentData: database is not initialized")
	}
	sqlDB, err := d.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// ReadinessChecks returns dependency readiness checks for /readyz.
func (d *Data) ReadinessChecks() []health.Check {
	return []health.Check{
		{
			Name: "postgres",
			Fn:   d.PingDB,
		},
	}
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
	// The count remains in the payload for older article consumers during rollout.
	if err := tx.Model(&Comment{}).Where("target = ? AND status = ?", aid, "active").Count(&count).Error; err != nil {
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
		Status:      outboxStatusPending,
		NextRetryAt: time.Now(),
	}).Error
}

func (d *Data) startOutbox() {
	interval := d.outboxInterval
	if interval <= 0 {
		interval = time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.outboxCancel = cancel
	d.outboxWG.Add(1)
	go func() {
		defer d.outboxWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := d.publishOutbox(ctx); err != nil && d.log != nil {
					d.log.WithContext(ctx).Errorf("commentData: publish outbox failed: %v", err)
				}
			}
		}
	}()
}

func (d *Data) getOutboxStore() outboxStore {
	if d.outboxStore != nil {
		return d.outboxStore
	}
	if d.db != nil {
		return &gormOutboxStore{db: d.db}
	}
	return nil
}

// publishOutbox decouples short DB transactions from Kafka network I/O:
// 1. Short DB TX: claim up to 50 due rows with FOR UPDATE SKIP LOCKED and set 60s lease.
// 2. Outside DB TX: publish each claimed event to Kafka with a 5s per-message timeout.
// 3. Short DB TX: mark succeeded rows published; increment retry_count on failures with
//    exponential backoff min(2^retry, 64)s or transition to dead_letter at retry_count >= 10.
// Loops up to 20 batches per tick while full batches are returned and ctx is active.
func (d *Data) publishOutbox(ctx context.Context) error {
	store := d.getOutboxStore()
	if d.event == nil || d.eventTopic == "" || store == nil {
		return nil
	}

	for iter := 0; iter < outboxMaxBatchesPerTick; iter++ {
		if ctx.Err() != nil {
			return nil
		}

		claimed, err := store.Claim(ctx, outboxBatchSize, outboxLeaseDuration)
		if err != nil {
			return err
		}
		if len(claimed) == 0 {
			return nil
		}

		outcomes := make([]outboxOutcome, 0, len(claimed))
		for _, event := range claimed {
			if ctx.Err() != nil {
				break
			}
			sendCtx, cancel := context.WithTimeout(ctx, outboxPublishTimeout)
			sendErr := d.event.Send(sendCtx, pkg.NewMessage(event.EventKey, event.Payload))
			cancel()

			now := time.Now()
			if sendErr != nil {
				retry := event.RetryCount + 1
				if retry >= outboxMaxRetries {
					if d.log != nil {
						d.log.WithContext(ctx).Errorf("commentData: outbox event %d (%s) moved to dead_letter after %d retries: %v",
							event.ID, event.EventID, retry, sendErr)
					}
					outcomes = append(outcomes, outboxOutcome{
						ID:         event.ID,
						Published:  false,
						Status:     outboxStatusDeadLetter,
						RetryCount: retry,
					})
				} else {
					outcomes = append(outcomes, outboxOutcome{
						ID:          event.ID,
						Published:   false,
						Status:      outboxStatusPending,
						RetryCount:  retry,
						NextRetryAt: now.Add(outboxBackoff(retry)),
					})
				}
				continue
			}

			outcomes = append(outcomes, outboxOutcome{
				ID:          event.ID,
				Published:   true,
				Status:      outboxStatusPublished,
				PublishedAt: now,
			})
		}

		if len(outcomes) > 0 {
			markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), outboxPublishTimeout)
			applyErr := store.ApplyOutcomes(markCtx, outcomes)
			cancel()
			if applyErr != nil {
				return applyErr
			}
		}

		if len(claimed) < outboxBatchSize {
			return nil
		}
	}

	return nil
}

func outboxBackoff(retry int) time.Duration {
	if retry < 1 {
		retry = 1
	}
	sec := 1 << minInt(retry, 6)
	if sec > outboxMaxBackoffSec {
		sec = outboxMaxBackoffSec
	}
	return time.Duration(sec) * time.Second
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
