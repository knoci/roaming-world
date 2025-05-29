package data

import (
	"github.com/knoci/roaming-world/scene/internal/conf"
	nacos "github.com/knoci/roaming-world/scene/internal/conf/nacos"
	kafka "github.com/knoci/roaming-world/scene/internal/pkg"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(
	NewData,
	NewSceneRepo,
)

// Data ..
type Data struct {
	db        *gorm.DB
	log       *log.Helper
	redis 	  *redis.Client
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
		log.Errorf("sceneData: failed to connect database: %v", err)
		return nil, nil, err
	}

	// 自动迁移表结构
	err = db.AutoMigrate(&Scene{})
	if err != nil {
		log.Errorf("sceneData: failed to auto migrate: %v", err)
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
		log.Error("sceneData: failed to create etcd client", err)
		return nil, nil, err
	}

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.Status(ctx, endpoints[0])
	if err != nil {
		log.Error("sceneData: failed to connect etcd", err)
		return nil, nil, err
	}

	bucketURL := nacos.GetConfigString(cfg, "cos.bucket_url")
	secretID := nacos.GetConfigString(cfg, "cos.secret_id")
	secretKey := nacos.GetConfigString(cfg, "cos.secret_key")

	// 检查配置是否存在
	if bucketURL == "" || secretID == "" || secretKey == "" {
		log.Error("sceneData: cos config missing")
		return nil, nil, fmt.Errorf("sceneData: cos config missing")
	}

	// 解析bucket URL
	u, err := url.Parse(bucketURL)
	if err != nil {
		log.Error("sceneData: parse cos Bucket URL failed", err)
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
		
		cdc:       cdc,
		logsender: logsender,
	}

	cleanup := func() {
		log.Info("sceneData: closing the data resources")
		sqlDB, err := d.db.DB()
		if err != nil {
			log.Errorf("user Data: failed to get sqlDB: %v", err)
			return
		}
		sqlDB.Close()
	}

	return d, cleanup, nil
}
