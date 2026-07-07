package data

import (
	"fmt"
	"strings"

	"github.com/glebarez/sqlite"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewDB, NewRedis)

// Data holds shared data-layer resources.
type Data struct {
	DB    *gorm.DB
	Redis *redis.Client
}

// openDialector selects the GORM dialect from config. Supported drivers:
// "mysql" (default), "postgres", "sqlite" (pure-Go, demo tier only).
func openDialector(c *conf.Database) (gorm.Dialector, error) {
	switch strings.ToLower(strings.TrimSpace(c.Driver)) {
	case "", "mysql":
		return mysql.Open(c.DSN), nil
	case "postgres", "postgresql":
		return postgres.Open(c.DSN), nil
	case "sqlite", "sqlite3":
		return sqlite.Open(c.DSN), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q (mysql / postgres / sqlite)", c.Driver)
	}
}

func NewDB(c *conf.Database, logger log.Logger) (*gorm.DB, error) {
	helper := log.NewHelper(logger)
	dialector, err := openDialector(c)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: gormLogger.Default.LogMode(gormLogger.Warn),
	})
	if err != nil {
		return nil, err
	}
	if err := autoMigrate(db); err != nil {
		helper.Errorf("DB auto-migrate failed: %v", err)
		return nil, err
	}
	helper.Info("DB connected and migrated")
	return db, nil
}

func autoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.AIProvider{},
		&model.AIModelItem{},
		&model.AIVirtualKey{},
		&model.AIModelMapping{},
		&model.AIVirtualKeyModelQuota{},
		&model.AIGatewayAuditLog{},
		&model.AIGatewayAuditLogBody{},
		&model.AIGatewayAuditLogFile{},
		&model.AIGatewayAuditFileObject{},
		&model.AIGatewayQuotaEvent{},
		&model.AIGatewayRouterEvent{},
		&model.AICreditsRate{},
		&model.AIPIIPolicy{},
		&model.AITenant{},
		&model.AIProject{},
		&model.AIBillingAccount{},
		&model.AIBillingLedger{},
		&model.AIUsageDaily{},
		&model.AIPriceTable{},
		&model.AIPriceTableItem{},
		&model.AISetting{},
	)
}

func NewRedis(c *conf.Redis, logger log.Logger) (*redis.Client, error) {
	helper := log.NewHelper(logger)
	rdb := redis.NewClient(&redis.Options{
		Addr:     c.Addr,
		Password: c.Password,
		DB:       c.DB,
	})
	helper.Info("Redis client created")
	return rdb, nil
}

func NewData(db *gorm.DB, rdb *redis.Client, logger log.Logger) (*Data, func(), error) {
	helper := log.NewHelper(logger)
	cleanup := func() {
		helper.Info("closing data resources")
		sqlDB, _ := db.DB()
		sqlDB.Close()
		rdb.Close()
	}
	return &Data{DB: db, Redis: rdb}, cleanup, nil
}
