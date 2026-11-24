package data

import (
	"context"
	"errors"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/knoci/roaming-world/pkg/dbx"
	"github.com/knoci/roaming-world/plan/internal/conf"
	nacos "github.com/knoci/roaming-world/plan/internal/conf/nacos"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var ProviderSet = wire.NewSet(NewData, NewPlanRepo)

type Data struct {
	db  *gorm.DB
	log *log.Helper
}

func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	loggerHelper := log.NewHelper(logger)
	cfg := nacos.GetConfig()
	dsn := dbx.BuildPostgresDSN(
		nacos.GetConfigString(cfg, "postgre.host"),
		nacos.GetConfigString(cfg, "postgre.user"),
		nacos.GetConfigString(cfg, "postgre.password"),
		nacos.GetConfigString(cfg, "postgre.dbname"),
		nacos.GetConfigInt(cfg, "postgre.port"),
		nacos.GetConfigString(cfg, "postgre.sslmode"),
	)
	db, err := dbx.OpenPostgres(dsn, &gorm.Config{
		NamingStrategy:                           schema.NamingStrategy{},
		DisableForeignKeyConstraintWhenMigrating: true,
	}, dbx.DefaultPoolConfig())
	if err != nil {
		loggerHelper.Errorf("planData: connect postgres: %v", err)
		return nil, nil, err
	}
	if dbx.ShouldAutoMigrate() {
		if err := db.AutoMigrate(&TravelPlan{}, &TravelPlanDay{}, &TravelPlanItem{}); err != nil {
			loggerHelper.Errorf("planData: auto migrate: %v", err)
			return nil, nil, err
		}
	}
	d := &Data{db: db, log: loggerHelper}
	cleanup := func() {
		sqlDB, err := d.db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}
	return d, cleanup, nil
}

// Ping verifies PostgreSQL connectivity for readiness probes.
func (d *Data) Ping(ctx context.Context) error {
	if d == nil || d.db == nil {
		return errors.New("planData: database is not initialized")
	}
	sqlDB, err := d.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

func (d *Data) LogSQLAudit(ctx context.Context, key, sql string, params []interface{}) error {
	d.log.WithContext(ctx).Debugf("sql audit: %s", sql)
	return nil
}

func (d *Data) LogError(ctx context.Context, key, errmsg, op string, data any) error {
	d.log.WithContext(ctx).Errorf("module=%s op=%s error=%s data=%v", key, op, errmsg, data)
	return nil
}
