package data

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/user/internal/conf"
	nacos "github.com/knoci/roaming-world/user/internal/conf/nacos"
	clientv3 "go.etcd.io/etcd/client/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewUserRepo)

// Data .
type Data struct {
	db   *gorm.DB
	etcd *clientv3.Client
	log  *log.Helper
}

// NewData .
func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	log := log.NewHelper(logger)

	cfg := nacos.GetConfig()
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s",
		getConfigString(cfg, "postgre.host"),
		getConfigString(cfg, "postgre.user"),
		getConfigString(cfg, "postgre.password"),
		getConfigString(cfg, "postgre.dbname"),
		getConfigInt(cfg, "postgre.port"),
		getConfigString(cfg, "postgre.sslmode"),
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
	endpoints := getConfigString(cfg, "etcd.endpoints"),
	dialTimeout := getConfigInt(cfg, "etcd.dialTimeout"),

	// 创建etcd客户端
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout * time.Second,
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

	}

	d := &Data{
		db:  db,
		log: log,
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

func getConfigString(cfg config.Config, key string) string {
	value, err := cfg.Value(key).String()
	if err != nil {
		log.Fatalf("Failed to get config %s: %v", key, err)
	}
	return value
}

func getConfigInt(cfg config.Config, key string) int {
	value, err := cfg.Value(key).Int()
	if err != nil {
		log.Fatalf("Failed to get config %s: %v", key, err)
	}
	return value
}
