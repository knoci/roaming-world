package dbx

import (
	"database/sql"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	defaultMaxOpenConns    = 25
	defaultMaxIdleConns    = 10
	defaultConnMaxLifetime = 30 * time.Minute
	defaultConnMaxIdleTime = 5 * time.Minute
	defaultSSLMode         = "disable"
)

// PoolConfig configures database/sql connection pool limits.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// DefaultPoolConfig returns conservative production connection pool defaults
// (25 open, 10 idle, 30m lifetime, 5m idle timeout), overridable via environment variables:
// DB_MAX_OPEN_CONNS, DB_MAX_IDLE_CONNS, DB_CONN_MAX_LIFETIME_SEC, DB_CONN_MAX_IDLE_TIME_SEC.
func DefaultPoolConfig() PoolConfig {
	cfg := PoolConfig{
		MaxOpenConns:    defaultMaxOpenConns,
		MaxIdleConns:    defaultMaxIdleConns,
		ConnMaxLifetime: defaultConnMaxLifetime,
		ConnMaxIdleTime: defaultConnMaxIdleTime,
	}

	if v := parsePositiveIntEnv("DB_MAX_OPEN_CONNS"); v > 0 {
		cfg.MaxOpenConns = v
	}
	if v := parsePositiveIntEnv("DB_MAX_IDLE_CONNS"); v > 0 {
		cfg.MaxIdleConns = v
	}
	if v := parsePositiveIntEnv("DB_CONN_MAX_LIFETIME_SEC"); v > 0 {
		cfg.ConnMaxLifetime = time.Duration(v) * time.Second
	}
	if v := parsePositiveIntEnv("DB_CONN_MAX_IDLE_TIME_SEC"); v > 0 {
		cfg.ConnMaxIdleTime = time.Duration(v) * time.Second
	}

	if cfg.MaxIdleConns > cfg.MaxOpenConns {
		cfg.MaxIdleConns = cfg.MaxOpenConns
	}
	return cfg
}

// BuildPostgresDSN constructs a URL-encoded PostgreSQL connection URI using net/url.URL,
// safely escaping special characters in usernames, passwords, database names, and query values.
func BuildPostgresDSN(host, user, password, dbname string, port int, sslmode string) string {
	host = strings.TrimSpace(host)
	dbname = strings.TrimPrefix(strings.TrimSpace(dbname), "/")
	sslmode = strings.TrimSpace(sslmode)
	if sslmode == "" {
		sslmode = defaultSSLMode
	}

	hostPort := host
	if port > 0 {
		hostPort = net.JoinHostPort(host, strconv.Itoa(port))
	}

	q := url.Values{}
	q.Set("sslmode", sslmode)

	u := &url.URL{
		Scheme:   "postgres",
		Host:     hostPort,
		Path:     "/" + dbname,
		RawQuery: q.Encode(),
	}
	if password != "" {
		u.User = url.UserPassword(user, password)
	} else if user != "" {
		u.User = url.User(user)
	}
	return u.String()
}

// OpenPostgres opens a GORM PostgreSQL connection and applies connection pool limits.
// Any zero or non-positive PoolConfig fields fall back to DefaultPoolConfig().
func OpenPostgres(dsn string, gormCfg *gorm.Config, pool PoolConfig) (*gorm.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("dbx: dsn is required")
	}
	if gormCfg == nil {
		gormCfg = &gorm.Config{}
	}

	db, err := gorm.Open(postgres.Open(dsn), gormCfg)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	applyPoolConfig(sqlDB, pool)
	return db, nil
}

// ShouldAutoMigrate returns true only when ROAMING_AUTO_MIGRATE is explicitly set to "true" (case-insensitive).
func ShouldAutoMigrate() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ROAMING_AUTO_MIGRATE")), "true")
}

func applyPoolConfig(sqlDB *sql.DB, pool PoolConfig) {
	defaults := DefaultPoolConfig()
	if pool.MaxOpenConns <= 0 {
		pool.MaxOpenConns = defaults.MaxOpenConns
	}
	if pool.MaxIdleConns <= 0 {
		pool.MaxIdleConns = defaults.MaxIdleConns
	}
	if pool.MaxIdleConns > pool.MaxOpenConns {
		pool.MaxIdleConns = pool.MaxOpenConns
	}
	if pool.ConnMaxLifetime <= 0 {
		pool.ConnMaxLifetime = defaults.ConnMaxLifetime
	}
	if pool.ConnMaxIdleTime <= 0 {
		pool.ConnMaxIdleTime = defaults.ConnMaxIdleTime
	}

	sqlDB.SetMaxOpenConns(pool.MaxOpenConns)
	sqlDB.SetMaxIdleConns(pool.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(pool.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(pool.ConnMaxIdleTime)
}

func parsePositiveIntEnv(key string) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return 0
	}
	return v
}
