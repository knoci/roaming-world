package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/pkg/dbx"
	"github.com/knoci/roaming-world/pkg/health"
	"github.com/knoci/roaming-world/pkg/nacosx"
	"github.com/knoci/roaming-world/scene/internal/conf"
	nacos "github.com/knoci/roaming-world/scene/internal/conf/nacos"
	"github.com/meilisearch/meilisearch-go"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const meiliSyncBatchSize = 200

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(
	NewData,
	NewHealthChecks,
	NewSceneRepo,
)

// Data ..
type Data struct {
	db    *gorm.DB
	log   *log.Helper
	redis *redis.Client
	meili meilisearch.ServiceManager
}

// NewData .
func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	helper := log.NewHelper(logger)

	cfg := nacos.GetConfig()
	dsn := dbx.BuildPostgresDSN(
		nacos.GetConfigString(cfg, "postgre.host"),
		nacos.GetConfigString(cfg, "postgre.user"),
		nacos.GetConfigString(cfg, "postgre.password"),
		nacos.GetConfigString(cfg, "postgre.dbname"),
		nacos.GetConfigInt(cfg, "postgre.port"),
		nacosx.GetStringOrDefault(cfg, "postgre.sslmode", "disable"),
	)

	// 连接数据库并配置连接池
	db, err := dbx.OpenPostgres(dsn, &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			NameReplacer: nil,
		},
		DisableForeignKeyConstraintWhenMigrating: true,
	}, dbx.DefaultPoolConfig())
	if err != nil {
		helper.Errorf("sceneData: failed to connect database: %v", err)
		return nil, nil, err
	}

	if dbx.ShouldAutoMigrate() {
		if err = db.AutoMigrate(&Scene{}); err != nil {
			helper.Errorf("sceneData: failed to auto migrate: %v", err)
			return nil, nil, err
		}
	}

	redisHost := nacos.GetConfigString(cfg, "redis.host")
	redisPort := nacos.GetConfigInt(cfg, "redis.port")
	redisPassword := nacosx.GetStringOrDefault(cfg, "redis.password", "")
	redisDB := nacosx.GetIntOrDefault(cfg, "redis.db", 0)

	rdb := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", redisHost, redisPort),
		Password:     redisPassword,
		DB:           redisDB,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelPing()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		helper.Errorf("sceneData: failed to connect redis: %v", err)
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		_ = rdb.Close()
		return nil, nil, err
	}

	// 初始化 MeiliSearch 客户端
	meiliHost := nacos.GetConfigString(cfg, "meili.host")
	meiliKey := nacosx.GetStringOrDefault(cfg, "meili.key", "")
	meili := meilisearch.New(meiliHost, meilisearch.WithAPIKey(meiliKey))

	// 初始化 MeiliSearch 索引
	sceneIndex := meili.Index("scenes")
	if _, err = sceneIndex.GetSettings(); err != nil {
		helper.Infof("Creating MeiliSearch index for scenes")
		if _, err = meili.CreateIndex(&meilisearch.IndexConfig{
			Uid:        "scenes",
			PrimaryKey: "sid",
		}); err != nil {
			helper.Warnf("Failed to create MeiliSearch index: %v", err)
		}

		if _, err = sceneIndex.UpdateSearchableAttributes(&[]string{
			"name",
			"describe",
			"location",
			"article",
		}); err != nil {
			helper.Warnf("Failed to update searchable attributes: %v", err)
		}

		if _, err = sceneIndex.UpdateFilterableAttributes(&[]string{
			"location",
			"updated_at",
		}); err != nil {
			helper.Warnf("Failed to update filterable attributes: %v", err)
		}

		if _, err = sceneIndex.UpdateSortableAttributes(&[]string{
			"name",
			"updated_at",
		}); err != nil {
			helper.Warnf("Failed to update sortable attributes: %v", err)
		}
	} else {
		helper.Infof("MeiliSearch index for scenes already exists")
	}

	d := &Data{
		db:    db,
		log:   helper,
		redis: rdb,
		meili: meili,
	}

	syncCtx, cancelSync := context.WithCancel(context.Background())
	// 异步按游标分批同步数据库中的场景到 MeiliSearch
	go d.syncScenesToMeiliSearch(syncCtx)

	cleanup := func() {
		cancelSync()
		helper.Info("sceneData: closing the data resources")
		if d.redis != nil {
			if err := d.redis.Close(); err != nil {
				helper.Errorf("sceneData: failed to close redis connection: %v", err)
			}
		}
		if d.db != nil {
			sqlDB, err := d.db.DB()
			if err != nil {
				helper.Errorf("sceneData: failed to get sqlDB: %v", err)
				return
			}
			if err := sqlDB.Close(); err != nil {
				helper.Errorf("sceneData: failed to close sqlDB: %v", err)
			}
		}
	}

	return d, cleanup, nil
}

// NewHealthChecks 返回场景服务就绪探针所需的依赖检查项。
func NewHealthChecks(d *Data) []health.Check {
	return []health.Check{
		{
			Name: "postgres",
			Fn: func(ctx context.Context) error {
				if d == nil || d.db == nil {
					return errors.New("postgres not initialized")
				}
				sqlDB, err := d.db.DB()
				if err != nil {
					return err
				}
				return sqlDB.PingContext(ctx)
			},
		},
		{
			Name: "redis",
			Fn: func(ctx context.Context) error {
				if d == nil || d.redis == nil {
					return errors.New("redis not initialized")
				}
				return d.redis.Ping(ctx).Err()
			},
		},
	}
}

func buildMeiliSceneDocument(scene *Scene) map[string]interface{} {
	return map[string]interface{}{
		"sid":        scene.SID,
		"name":       scene.Name,
		"describe":   scene.Describe,
		"location":   scene.Location,
		"article":    scene.Article,
		"view":       scene.View,
		"created_at": scene.CreatedAt.Format(time.RFC3339),
		"updated_at": scene.UpdatedAt.Format(time.RFC3339),
	}
}

// syncScenesToMeiliSearch 以 sid 游标分批将数据库中的场景同步到 MeiliSearch。
func (d *Data) syncScenesToMeiliSearch(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil && d != nil && d.log != nil {
			d.log.Errorf("sceneData: panic recovered in syncScenesToMeiliSearch: %v", rec)
		}
	}()
	if d == nil || d.db == nil || d.meili == nil {
		return
	}

	d.log.Info("Starting cursor-batched sync of scenes to MeiliSearch")

	var (
		lastSID     string
		totalSynced int
	)

	for {
		if ctx.Err() != nil {
			d.log.Warnf("MeiliSearch scene sync canceled after %d scenes: %v", totalSynced, ctx.Err())
			return
		}

		var batch []Scene
		q := d.db.WithContext(ctx).Order("sid ASC").Limit(meiliSyncBatchSize)
		if lastSID != "" {
			q = q.Where("sid > ?", lastSID)
		}
		if err := q.Find(&batch).Error; err != nil {
			d.log.Errorf("Failed to fetch scene batch from database: %v", err)
			return
		}
		if len(batch) == 0 {
			break
		}

		documents := make([]map[string]interface{}, len(batch))
		for i := range batch {
			documents[i] = buildMeiliSceneDocument(&batch[i])
		}

		if _, err := d.meili.Index("scenes").AddDocuments(documents, "sid"); err != nil {
			d.log.Errorf("Failed to add scene batch to MeiliSearch: %v", err)
			return
		}

		totalSynced += len(batch)
		lastSID = batch[len(batch)-1].SID
		if len(batch) < meiliSyncBatchSize {
			break
		}
	}

	d.log.Infof("Successfully synced %d scenes to MeiliSearch", totalSynced)
}

func (d *Data) LogSQLAudit(ctx context.Context, key string, sql string, params []interface{}) error {
	if d == nil || d.log == nil {
		return nil
	}
	d.log.WithContext(ctx).Debugf("sql audit: %s", sql)
	return nil
}

func (d *Data) LogError(ctx context.Context, key string, errmsg string, errop string, errdata any) error {
	if d == nil || d.log == nil {
		return nil
	}
	d.log.WithContext(ctx).Errorf("module=%s op=%s error=%s data=%v", key, errop, errmsg, errdata)
	return nil
}
