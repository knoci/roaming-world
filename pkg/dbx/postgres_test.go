package dbx

import (
	"database/sql"
	"database/sql/driver"
	"net/url"
	"testing"
	"time"
)

func TestDefaultPoolConfigAndEnvOverrides(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "")
	t.Setenv("DB_MAX_IDLE_CONNS", "")
	t.Setenv("DB_CONN_MAX_LIFETIME_SEC", "")
	t.Setenv("DB_CONN_MAX_IDLE_TIME_SEC", "")

	def := DefaultPoolConfig()
	if def.MaxOpenConns != 25 || def.MaxIdleConns != 10 ||
		def.ConnMaxLifetime != 30*time.Minute || def.ConnMaxIdleTime != 5*time.Minute {
		t.Fatalf("unexpected defaults: %+v", def)
	}

	// Custom valid overrides.
	t.Setenv("DB_MAX_OPEN_CONNS", "50")
	t.Setenv("DB_MAX_IDLE_CONNS", "20")
	t.Setenv("DB_CONN_MAX_LIFETIME_SEC", "600")
	t.Setenv("DB_CONN_MAX_IDLE_TIME_SEC", "120")

	custom := DefaultPoolConfig()
	if custom.MaxOpenConns != 50 || custom.MaxIdleConns != 20 ||
		custom.ConnMaxLifetime != 10*time.Minute || custom.ConnMaxIdleTime != 2*time.Minute {
		t.Fatalf("unexpected custom pool config: %+v", custom)
	}

	// MaxIdleConns clamped when exceeding MaxOpenConns.
	t.Setenv("DB_MAX_OPEN_CONNS", "5")
	t.Setenv("DB_MAX_IDLE_CONNS", "15")
	clamped := DefaultPoolConfig()
	if clamped.MaxOpenConns != 5 || clamped.MaxIdleConns != 5 {
		t.Fatalf("expected idle clamped to 5, got %+v", clamped)
	}

	// Invalid / non-positive values ignored.
	t.Setenv("DB_MAX_OPEN_CONNS", "-1")
	t.Setenv("DB_MAX_IDLE_CONNS", "abc")
	t.Setenv("DB_CONN_MAX_LIFETIME_SEC", "0")
	t.Setenv("DB_CONN_MAX_IDLE_TIME_SEC", "nope")
	fallback := DefaultPoolConfig()
	if fallback != def {
		t.Fatalf("expected invalid env vars to fall back to defaults %+v, got %+v", def, fallback)
	}
}

func TestBuildPostgresDSN(t *testing.T) {
	dsn := BuildPostgresDSN("db.internal", "roaming", "simplepass", "user_db", 5432, "require")
	want := "postgres://roaming:simplepass@db.internal:5432/user_db?sslmode=require"
	if dsn != want {
		t.Fatalf("got %q, want %q", dsn, want)
	}

	// Special characters in password & username must be safely URL-encoded and round-trip via url.Parse.
	specialPass := `p@ss:w/rd?&#% =+!$`
	specialDSN := BuildPostgresDSN("10.0.0.8", "app+user", specialPass, "scene_db", 5433, "")
	parsed, err := url.Parse(specialDSN)
	if err != nil {
		t.Fatalf("url.Parse(%q) failed: %v", specialDSN, err)
	}
	if parsed.Scheme != "postgres" || parsed.Host != "10.0.0.8:5433" || parsed.Path != "/scene_db" {
		t.Fatalf("unexpected parsed components: %+v", parsed)
	}
	if parsed.User.Username() != "app+user" {
		t.Fatalf("unexpected username %q", parsed.User.Username())
	}
	gotPass, hasPass := parsed.User.Password()
	if !hasPass || gotPass != specialPass {
		t.Fatalf("password round-trip mismatch: got (%q, %v), want %q", gotPass, hasPass, specialPass)
	}
	if parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("expected default sslmode=disable, got %q", parsed.Query().Get("sslmode"))
	}

	// IPv6 host + no password.
	ipv6DSN := BuildPostgresDSN("::1", "pguser", "", "/mydb", 5432, "verify-full")
	wantIPv6 := "postgres://pguser@[::1]:5432/mydb?sslmode=verify-full"
	if ipv6DSN != wantIPv6 {
		t.Fatalf("got %q, want %q", ipv6DSN, wantIPv6)
	}
}

func TestShouldAutoMigrate(t *testing.T) {
	for _, tc := range []struct {
		val  string
		want bool
	}{
		{"", false},
		{"false", false},
		{"1", false},
		{"yes", false},
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{" true ", true},
	} {
		t.Setenv("ROAMING_AUTO_MIGRATE", tc.val)
		if got := ShouldAutoMigrate(); got != tc.want {
			t.Fatalf("ROAMING_AUTO_MIGRATE=%q: got %v, want %v", tc.val, got, tc.want)
		}
	}
}

type noopDriver struct{}

func (noopDriver) Open(string) (driver.Conn, error) { return noopConn{}, nil }

type noopConn struct{}

func (noopConn) Prepare(string) (driver.Stmt, error) { return nil, nil }
func (noopConn) Close() error                        { return nil }
func (noopConn) Begin() (driver.Tx, error)           { return nil, nil }

func TestApplyPoolConfigAndOpenPostgresErrors(t *testing.T) {
	if _, err := OpenPostgres("", nil, DefaultPoolConfig()); err == nil {
		t.Fatalf("expected error for empty DSN")
	}

	sql.Register("dbx_test_noop", noopDriver{})
	sqlDB, err := sql.Open("dbx_test_noop", "")
	if err != nil {
		t.Fatalf("sql.Open noop driver: %v", err)
	}
	defer sqlDB.Close()

	// Custom pool settings applied.
	applyPoolConfig(sqlDB, PoolConfig{
		MaxOpenConns:    17,
		MaxIdleConns:    8,
		ConnMaxLifetime: 10 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	})
	if got := sqlDB.Stats().MaxOpenConnections; got != 17 {
		t.Fatalf("expected MaxOpenConnections=17, got %d", got)
	}

	// Zero-value PoolConfig falls back to DefaultPoolConfig.
	t.Setenv("DB_MAX_OPEN_CONNS", "")
	t.Setenv("DB_MAX_IDLE_CONNS", "")
	applyPoolConfig(sqlDB, PoolConfig{})
	if got := sqlDB.Stats().MaxOpenConnections; got != defaultMaxOpenConns {
		t.Fatalf("expected fallback MaxOpenConnections=%d, got %d", defaultMaxOpenConns, got)
	}
}
