package conf

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
)

func TestNacosWrapperDelegatesToPkgNacosx(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.yaml")
	content := []byte("redis:\n  host: 127.0.0.1\n  port: 6379\n  db: 2\njwt:\n  access_ttl: 15m\n")
	if err := os.WriteFile(cfgPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	c := config.New(config.WithSource(file.NewSource(cfgPath)))
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if got := GetConfigString(c, "redis.host"); got != "127.0.0.1" {
		t.Fatalf("expected redis.host=127.0.0.1, got %q", got)
	}
	if got := GetConfigInt(c, "redis.port"); got != 6379 {
		t.Fatalf("expected redis.port=6379, got %d", got)
	}
	if got := GetStringOrDefault(c, "redis.password", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback password, got %q", got)
	}
	if got := GetIntOrDefault(c, "redis.db", 0); got != 2 {
		t.Fatalf("expected redis.db=2, got %d", got)
	}
	if got := GetIntOrDefault(c, "redis.missing", 9); got != 9 {
		t.Fatalf("expected default 9, got %d", got)
	}
	if got := GetDurationOrDefault(c, "jwt.access_ttl", 30*time.Minute); got != 15*time.Minute {
		t.Fatalf("expected 15m, got %v", got)
	}
	if got := GetDurationOrDefault(c, "jwt.missing_ttl", 30*time.Minute); got != 30*time.Minute {
		t.Fatalf("expected default 30m, got %v", got)
	}
}
