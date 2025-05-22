package data

import (
	"fmt"
	"github.com/knoci/roaming-world/user/internal/conf"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewUserRepo)

// Data .
type Data struct {
	db  *gorm.DB
	log *log.Helper
}

// NewData .
func NewData(c *conf.Data, logger log.Logger) (*Data, func(), error) {
	log := log.NewHelper(logger)

	// 构建PostgreSQL连接字符串
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=disable",
		nacos.GetConfig().Value("postgre.host"),
		c.Database.Username,
		c.Database.Password,
		c.Database.Dbname,
		c.Database.Port,
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
	err = db.AutoMigrate(&User{}, &VerificationCode{})
	if err != nil {
		log.Errorf("failed to auto migrate: %v", err)
		return nil, nil, err
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
