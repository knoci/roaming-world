package data

import (
	"comment/internal/pkg"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v9"
	"github.com/google/uuid"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/comment/internal/conf"
	nacos "github.com/knoci/roaming-world/comment/internal/conf/nacos"
	kafka "github.com/knoci/roaming-world/comment/internal/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Comment 评论模型
type Comment struct {
	CID       string    `gorm:"primaryKey;type:varchar(36);column:cid" json:"cid"`
	Target    string    `gorm:"type:varchar(36)" json:"target"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	Likes     int       `gorm:"default:0" json:"likes"`
	UID       string    `gorm:"type:varchar(36);column:uid" json:"uid"`
	Name      string    `gorm:"type:varchar(50);not null" json:"name"`
	Avatar    string    `gorm:"type:varchar(100)" json:"avatar"`
	Replycid  string    `gorm:"type:varchar(36)" json:"replycid"`
	Replyname string    `gorm:"type:varchar(36)" json:"replyname"`
	Time      string    `gorm:"type:varchar(36)" json:"time"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (c *Comment) BeforeCreate(tx *gorm.DB) error {
	if c.CID == "" {
		c.CID = uuid.New().String()
	}
	return nil
}

// Commentlike 评论点赞模型
type Commentlike struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UID       string    `gorm:"type:varchar(36);column:uid" json:"uid"`
	CID       string    `gorm:"type:varchar(36);column:cid" json:"cid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewGreeterRepo, NewCommentRepo)

// Message 定义Kafka消息结构
type Message struct {
	Key   string
	Value []byte
}

// NewMessage 创建新的Kafka消息
func NewMessage(key string, value []byte) *Message {
	return &Message{Key: key, Value: value}
}

// Data 数据层结构体
type Data struct {
	db        *gorm.DB
	rdb       *redis.Client
	cdc       *pkg.KafkaSender
	logsender *pkg.KafkaSender
	log       *log.Helper
}

// NewData 初始化数据层资源
func NewData(c *conf.Bootstrap, logger log.Logger) (*Data, func(), error) {
	l := log.NewHelper(logger)

	// 从Nacos获取数据库配置
	dbConfig, err := getDBConfig()
	if err != nil {
		l.Errorf("failed to get database config from nacos: %v", err)
		return nil, nil, err
	}

	// 初始化数据库连接
	db, err := initDB(dbConfig, logger)
	if err != nil {
		l.Errorf("failed to connect to database: %v", err)
		return nil, nil, err
	}

	// 自动迁移数据库表结构
	if err := db.AutoMigrate(&Comment{}, &Commentlike{}); err != nil {
		l.Errorf("failed to auto migrate tables: %v", err)
		return nil, nil, err
	}

	// 从Nacos获取Redis配置
	redisConfig, err := getRedisConfig()
	if err != nil {
		l.Errorf("failed to get redis config from nacos: %v", err)
		return nil, nil, err
	}

	// 初始化Redis连接
	rdb := redis.NewClient(&redis.Options{
		Addr:         redisConfig.Addr,
		Password:     "", // 如果有密码，从配置中获取
		DB:           0,
		ReadTimeout:  time.Duration(redisConfig.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(redisConfig.WriteTimeout) * time.Second,
	})

	// 测试Redis连接
	if _, err := rdb.Ping(context.Background()).Result(); err != nil {
		l.Errorf("failed to connect to redis: %v", err)
		return nil, nil, err
	}

	// 从Nacos获取Kafka配置
	kafkaConfig, err := getKafkaConfig()
	if err != nil {
		l.Errorf("failed to get kafka config from nacos: %v", err)
		return nil, nil, err
	}

	// 初始化Kafka CDC发送器
	cdc, err := pkg.NewKafkaSender(
		kafkaConfig.Brokers,
		kafkaConfig.CDCTopic,
		kafka.RoundRobin,
		100,
		1*time.Second,
		kafka.CompressionSnappy,
	)
	if err != nil {
		l.Errorf("failed to create kafka cdc sender: %v", err)
		return nil, nil, err
	}

	// 初始化Kafka日志发送器
	logsender, err := pkg.NewKafkaSender(
		kafkaConfig.Brokers,
		kafkaConfig.LogTopic,
		kafka.RoundRobin,
		100,
		1*time.Second,
		kafka.CompressionSnappy,
	)
	if err != nil {
		l.Errorf("failed to create kafka log sender: %v", err)
		return nil, nil, err
	}

	d := &Data{
		db:        db,
		rdb:       rdb,
		cdc:       cdc,
		logsender: logsender,
		log:       l,
	}

	// 清理函数
	cleanup := func() {
		l.Info("closing the data resources")
		if err := rdb.Close(); err != nil {
			l.Errorf("failed to close redis client: %v", err)
		}
		if err := cdc.Close(); err != nil {
			l.Errorf("failed to close kafka cdc sender: %v", err)
		}
		if err := logsender.Close(); err != nil {
			l.Errorf("failed to close kafka log sender: %v", err)
		}
	}

	return d, cleanup, nil
}

// 从Nacos获取数据库配置
func getDBConfig() (*conf.Data_Database, error) {
	config := nacos.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("nacos config is nil")
	}

	var data struct {
		Data struct {
			Database conf.Data_Database `json:"database"`
		} `json:"data"`
	}

	if err := config.UnmarshalKey("data", &data.Data); err != nil {
		return nil, err
	}

	return &data.Data.Database, nil
}

// 从Nacos获取Redis配置
func getRedisConfig() (*conf.Data_Redis, error) {
	config := nacos.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("nacos config is nil")
	}

	var data struct {
		Data struct {
			Redis conf.Data_Redis `json:"redis"`
		} `json:"data"`
	}

	if err := config.UnmarshalKey("data", &data.Data); err != nil {
		return nil, err
	}

	return &data.Data.Redis, nil
}

// 从Nacos获取Kafka配置
func getKafkaConfig() (*struct {
	Brokers  []string
	CDCTopic string
	LogTopic string
}, error) {
	config := nacos.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("nacos config is nil")
	}

	var data struct {
		Data struct {
			Kafka struct {
				Brokers  []string `json:"brokers"`
				CDCTopic string   `json:"cdc_topic"`
				LogTopic string   `json:"log_topic"`
			} `json:"kafka"`
		} `json:"data"`
	}

	if err := config.UnmarshalKey("data", &data.Data); err != nil {
		return nil, err
	}

	return &struct {
		Brokers  []string
		CDCTopic string
		LogTopic string
	}{
		Brokers:  data.Data.Kafka.Brokers,
		CDCTopic: data.Data.Kafka.CDCTopic,
		LogTopic: data.Data.Kafka.LogTopic,
	}, nil
}

// 初始化数据库连接
func initDB(dbConfig *conf.Data_Database, logger log.Logger) (*gorm.DB, error) {
	l := log.NewHelper(logger)

	// 配置GORM日志
	newLogger := logger.NewHelper(log.With(logger, "module", "gorm"))
	gormLogger := logger.LogMode(logger.Info)
	if dbConfig.Driver == "postgres" {
		l.Info("connecting to postgres database")
		dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s",
			dbConfig.Host, dbConfig.Username, dbConfig.Password, dbConfig.Dbname, dbConfig.Port, dbConfig.Sslmode)
		return gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: gormLogger,
		})
	} else if dbConfig.Driver == "mysql" {
		l.Info("connecting to mysql database")
		return gorm.Open(mysql.Open(dbConfig.Source), &gorm.Config{
			Logger: gormLogger,
		})
	}

	return nil, fmt.Errorf("unsupported database driver: %s", dbConfig.Driver)
}

// SendSqlLog 发送SQL日志到Kafka
func (d *Data) SendSqlLog(ctx context.Context, key, sql string, params []interface{}) error {
	msg := struct {
		Query  string        `json:"query"`
		Params []interface{} `json:"params"`
	}{
		Query:  sql,
		Params: params,
	}

	sqlbyte, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return d.cdc.Send(ctx, NewMessage(key, sqlbyte))
}

// SendErrorLog 发送错误日志到Kafka
func (d *Data) SendErrorLog(ctx context.Context, key, errmsg, errop string, errdata interface{}) error {
	msg := struct {
		ErrorMsg  string      `json:"error_msg"`
		ErrorOp   string      `json:"error_op"`
		ErrorData interface{} `json:"error_data"`
	}{
		ErrorMsg:  errmsg,
		ErrorOp:   errop,
		ErrorData: errdata,
	}

	errbyte, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return d.logsender.Send(ctx, NewMessage(key, errbyte))
}
