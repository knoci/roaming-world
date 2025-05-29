package data

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/user/internal/conf"
	nacos "github.com/knoci/roaming-world/user/internal/conf/nacos"
	kafka "github.com/knoci/roaming-world/user/internal/pkg"
	"github.com/tencentyun/cos-go-sdk-v5"
	clientv3 "go.etcd.io/etcd/client/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewUserRepo)

// Data .
type Data struct {
	db        *gorm.DB
	etcd      *clientv3.Client
	log       *log.Helper
	cos       *cos.Client
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
		log.Errorf("userData: failed to connect database: %v", err)
		return nil, nil, err
	}

	// 自动迁移表结构
	err = db.AutoMigrate(&User{})
	if err != nil {
		log.Errorf("userData: failed to auto migrate: %v", err)
		return nil, nil, err
	}

	// 获取etcd配置
	endpoint := nacos.GetConfigString(cfg, "etcd.endpoints")
	endpoints := []string{endpoint}
	dialTimeout := nacos.GetConfigInt(cfg, "etcd.dialTimeout")

	// 创建etcd客户端
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: time.Second * time.Duration(dialTimeout),
	})

	if err != nil {
		log.Error("userData: failed to create etcd client", err)
		return nil, nil, err
	}

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.Status(ctx, endpoints[0])
	if err != nil {
		log.Error("userData: failed to connect etcd", err)
		return nil, nil, err
	}

	bucketURL := nacos.GetConfigString(cfg, "cos.bucket_url")
	secretID := nacos.GetConfigString(cfg, "cos.secret_id")
	secretKey := nacos.GetConfigString(cfg, "cos.secret_key")

	// 检查配置是否存在
	if bucketURL == "" || secretID == "" || secretKey == "" {
		log.Error("userData: cos config missing")
		return nil, nil, fmt.Errorf("userData: cos config missing")
	}

	// 解析bucket URL
	u, err := url.Parse(bucketURL)
	if err != nil {
		log.Error("userData: parse cos Bucket URL failed", err)
		return nil, nil, err
	}

	// 创建COS客户端
	b := &cos.BaseURL{BucketURL: u}
	cos := cos.NewClient(b, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretID,
			SecretKey: secretKey,
		},
	})

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

	d := &Data{
		db:        db,
		log:       log,
		etcd:      client,
		cos:       cos,
		cdc:       cdc,
		logsender: logsender,
	}

	cleanup := func() {
		log.Info("userData: closing the data resources")
		sqlDB, err := d.db.DB()
		if err != nil {
			log.Errorf("user Data: failed to get sqlDB: %v", err)
			return
		}
		sqlDB.Close()
	}

	return d, cleanup, nil
}

func (d *Data) SetEtcd(ctx context.Context, key string, time int64) error {
	lease, err := d.etcd.Grant(ctx, 600) // 10分钟 = 600秒
	if err != nil {
		return err
	}

	// 使用租约存储验证码，值设为1
	_, err = d.etcd.Put(ctx, key, "1", clientv3.WithLease(lease.ID))
	if err != nil {
		return err
	}
	return nil
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
