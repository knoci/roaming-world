package data

import (
	"context"
	"errors"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/food/internal/conf"
	nacos "github.com/knoci/roaming-world/food/internal/conf/nacos"
	"github.com/knoci/roaming-world/pkg/dbx"
	"github.com/knoci/roaming-world/pkg/health"
	"github.com/knoci/roaming-world/pkg/nacosx"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(
	NewData,
	NewHealthChecks,
	NewFoodRepo,
)

// Data .
type Data struct {
	db  *gorm.DB
	log *log.Helper
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
		helper.Errorf("foodData: failed to connect database: %v", err)
		return nil, nil, err
	}

	if dbx.ShouldAutoMigrate() {
		if err = db.AutoMigrate(&Food{}); err != nil {
			helper.Errorf("foodData: failed to auto migrate: %v", err)
			return nil, nil, err
		}
	}

	d := &Data{db: db, log: helper}

	cleanup := func() {
		helper.Info("foodData: closing the data resources")
		if d.db != nil {
			sqlDB, err := d.db.DB()
			if err != nil {
				helper.Errorf("foodData: failed to get sqlDB: %v", err)
				return
			}
			if err := sqlDB.Close(); err != nil {
				helper.Errorf("foodData: failed to close sqlDB: %v", err)
			}
		}
	}

	return d, cleanup, nil
}

// NewHealthChecks 返回美食服务就绪探针所需的依赖检查项。
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
	}
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
