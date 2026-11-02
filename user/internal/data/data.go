package data

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	nacos "github.com/go-kratos/kratos/contrib/registry/nacos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/user/internal/conf"
	nc "github.com/knoci/roaming-world/user/internal/conf/nacos"
	"github.com/tencentyun/cos-go-sdk-v5"
	clientv3 "go.etcd.io/etcd/client/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewUserRepo, NewNacosEngine)

// Data .
type Data struct {
	db   *gorm.DB
	etcd *clientv3.Client
	log  *log.Helper
	cos  *cos.Client
}

// NewData .
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
		log.Errorf("userData: failed to connect database: %v", err)
		return nil, nil, err
	}

	// 自动迁移表结构
	err = db.AutoMigrate(&User{}, &Follow{}, &Notification{})
	if err != nil {
		log.Errorf("userData: failed to auto migrate: %v", err)
		return nil, nil, err
	}

	// 获取etcd配置
	endpoint := nc.GetConfigString(cfg, "etcd.endpoints")
	endpoints := []string{endpoint}
	dialTimeout := nc.GetConfigInt(cfg, "etcd.dialTimeout")

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

	bucketURL := nc.GetConfigString(cfg, "cos.bucket_url")
	secretID := nc.GetConfigString(cfg, "cos.secret_id")
	secretKey := nc.GetConfigString(cfg, "cos.secret_key")

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

	d := &Data{
		db:   db,
		log:  log,
		etcd: client,
		cos:  cos,
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

func (d *Data) LogSQLAudit(ctx context.Context, key string, sql string, params []interface{}) error {
	d.log.WithContext(ctx).Debugf("sql audit: %s", sql)
	return nil
}

func (d *Data) LogError(ctx context.Context, key string, errmsg string, errop string, errdata any) error {
	d.log.WithContext(ctx).Errorf("module=%s op=%s error=%s data=%v", key, errop, errmsg, errdata)
	return nil
}

func NewNacosEngine(c *conf.Data) *nacos.Registry {
	return nc.NewRegistry()
}
