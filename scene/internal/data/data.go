package data

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/scene/internal/conf"
	nacos "github.com/knoci/roaming-world/scene/internal/conf/nacos"
	kafka "github.com/knoci/roaming-world/scene/internal/pkg"
	"github.com/meilisearch/meilisearch-go"
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
	redis     *redis.Client
	cdc       *kafka.KafkaSender
	logsender *kafka.KafkaSender
	meili     meilisearch.ServiceManager
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

	// 初始化MeiliSearch客户端
	meiliHost := nacos.GetConfigString(cfg, "meili.host")
	meiliKey := nacos.GetConfigString(cfg, "meili.key")
	meili := meilisearch.New(meiliHost, meilisearch.WithAPIKey(meiliKey))

	// 初始化MeiliSearch索引
	sceneIndex := meili.Index("scenes")

	// 创建索引（如果不存在）
	_, err = sceneIndex.GetSettings()
	if err != nil {
		log.Infof("Creating MeiliSearch index for scenes")
		// 设置索引配置
		_, err = meili.CreateIndex(&meilisearch.IndexConfig{
			Uid:        "scenes",
			PrimaryKey: "sid",
		})
		if err != nil {
			log.Warnf("Failed to create MeiliSearch index: %v", err)
		}

		// 设置可搜索的属性
		_, err = sceneIndex.UpdateSearchableAttributes(&[]string{
			"name",
			"describe",
			"location",
			"article",
		})
		if err != nil {
			log.Warnf("Failed to update searchable attributes: %v", err)
		}

		// 设置可过滤的属性
		_, err = sceneIndex.UpdateFilterableAttributes(&[]string{
			"location",
			"updated_at",
		})
		if err != nil {
			log.Warnf("Failed to update filterable attributes: %v", err)
		}

		// 设置排序属性
		_, err = sceneIndex.UpdateSortableAttributes(&[]string{
			"name",
			"updated_at",
		})
		if err != nil {
			log.Warnf("Failed to update sortable attributes: %v", err)
		}
	} else {
		log.Infof("MeiliSearch index for scenes already exists")
	}

	// 创建Data实例
	d := &Data{
		db:        db,
		log:       log,
		cdc:       cdc,
		logsender: logsender,
		meili:     meili,
	}

	// 异步同步数据库中的场景到MeiliSearch
	go d.syncScenesToMeiliSearch(context.Background())

	redisHost := nacos.GetConfigString(cfg, "redis.host")
	redisPort := nacos.GetConfigInt(cfg, "redis.port")
	redisPassword := nacos.GetConfigString(cfg, "redis.password")
	redisDB := nacos.GetConfigInt(cfg, "redis.db")

	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", redisHost, redisPort),
		Password: redisPassword,
		DB:       redisDB,
	})
	// 测试连接
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Error("sceneData: failed to connect redis", err)
		return nil, nil, err
	}

	// 更新Data实例，添加redis
	d.redis = rdb

	cleanup := func() {
		log.Info("sceneData: closing the data resources")
		sqlDB, err := d.db.DB()
		if err != nil {
			log.Errorf("user Data: failed to get sqlDB: %v", err)
			return
		}
		sqlDB.Close()
		err = rdb.Close()
		if err != nil {
			log.Error("failed to close redis connection", err)
		}
	}

	return d, cleanup, nil
}

// syncScenesToMeiliSearch 将数据库中的所有场景同步到MeiliSearch
func (d *Data) syncScenesToMeiliSearch(ctx context.Context) {
	d.log.Info("Starting to sync scenes to MeiliSearch")

	// 从数据库获取所有场景
	var scenes []Scene
	result := d.db.Find(&scenes)
	if result.Error != nil {
		d.log.Errorf("Failed to fetch scenes from database: %v", result.Error)
		return
	}

	d.log.Infof("Found %d scenes to sync to MeiliSearch", len(scenes))

	// 如果没有场景，直接返回
	if len(scenes) == 0 {
		d.log.Info("No scenes to sync to MeiliSearch")
		return
	}

	// 准备MeiliSearch文档
	documents := make([]map[string]interface{}, len(scenes))
	for i, scene := range scenes {
		documents[i] = map[string]interface{}{
			"sid":        scene.SID,
			"name":       scene.Name,
			"describe":   scene.Describe,
			"location":   scene.Location,
			"article":    scene.Article,
			"view":       scene.View,
			"updated_at": scene.UpdatedAt,
			"created_at": scene.CreatedAt,
		}
	}

	// 批量添加文档到MeiliSearch
	_, err := d.meili.Index("scenes").AddDocuments(documents)
	if err != nil {
		d.log.Errorf("Failed to add documents to MeiliSearch: %v", err)
		return
	}

	d.log.Info("Successfully synced scenes to MeiliSearch")

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
