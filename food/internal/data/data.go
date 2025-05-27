package data

import (
	"context"
	"fmt"
	"time"
	"net/url"
	"net/http"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/food/internal/conf"
	kafka "github.com/knoci/roaming-world/food/internal/pkg"
	nacos "github.com/knoci/roaming-world/food/internal/conf/nacos"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewFoodRepo)

// Data .
type Data struct {
	db   *gorm.DB
	log  *log.Helper
	kafka *kafka.KafkaSender
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
		log.Errorf("failed to connect database: %v", err)
		return nil, nil, err
	}

	// 自动迁移表结构
	err = db.AutoMigrate(&User{})
	if err != nil {
		log.Errorf("failed to auto migrate: %v", err)
		return nil, nil, err
	}

	// 从viper获取etcd配置
	endpoint := nacos.GetConfigString(cfg, "etcd.endpoints")
	endpoints := []string{endpoint}
	dialTimeout := nacos.GetConfigInt(cfg, "etcd.dialTimeout")

	// 创建etcd客户端
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: time.Second * time.Duration(dialTimeout),
	})

	if err != nil {
		log.Error("failed to create etcd client", err)
		return nil, nil, err
	}

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.Status(ctx, endpoints[0])
	if err != nil {
		log.Error("failed to connect etcd", err)
		return nil, nil, err
	}

	bucketURL := nacos.GetConfigString(cfg, "cos.bucket_url")
	secretID := nacos.GetConfigString(cfg, "cos.secret_id")
	secretKey := nacos.GetConfigString(cfg, "cos.secret_key")

	// 检查配置是否存在
	if bucketURL == "" || secretID == "" || secretKey == "" {
		log.Error("cos config missing")
		return nil, nil, fmt.Errorf("cos config missing")
	}

	// 解析bucket URL
	u, err := url.Parse(bucketURL)
	if err != nil {
		log.Error("parse cos Bucket URL failed", err)
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

	address := nacos.GetConfigString(cfg, "kafka.address")
	topic := nacos.GetConfigString(cfg, "kafka.topic")
	sender, err := kafka.NewKafkaSender([]string{address}, topic)
	if err != nil {
		panic(err)
	}

	d := &Data{
		db:  db,
		log: log,
		cos: cos,
		kafka: sender,
	}

	cleanup := func() {
		log.Info("closing the data resources")
		sqlDB, err := d.db.DB()
		if err != nil {
			log.Errorf("failed to get sqlDB: %v", err)
			return
		}
		sqlDB.Close()
	}

	return d, cleanup, nil
}
