package data

import (
	"scene/internal/conf"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(
	NewData,
	NewSceneRepo,
)

// Data ..
type Data struct {
	db *gorm.DB
}

// NewData .
func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	log := log.NewHelper(logger)

	// 初始化MySQL连接
	db, err := gorm.Open(mysql.Open(c.Database.Source), &gorm.Config{})
	if err != nil {
		log.Errorf("failed opening connection to mysql: %v", err)
		return nil, nil, err
	}

	// 自动迁移表结构
	if err := db.AutoMigrate(&Scene{}); err != nil {
		log.Errorf("failed to auto migrate: %v", err)
		return nil, nil, err
	}

	d := &Data{
		db: db,
	}

	return d, func() {
		log.Info("closing the data resources")
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}, nil
}
