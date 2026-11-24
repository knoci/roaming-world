package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
)

func TestNacosWrapperDelegatesToPkgNacosx(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.yaml")
	content := []byte("postgre:\n  host: 127.0.0.1\n  port: 5432\n")
	if err := os.WriteFile(cfgPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	c := config.New(config.WithSource(file.NewSource(cfgPath)))
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if got := GetConfigString(c, "postgre.host"); got != "127.0.0.1" {
		t.Fatalf("expected postgre.host=127.0.0.1, got %q", got)
	}
	if got := GetConfigInt(c, "postgre.port"); got != 5432 {
		t.Fatalf("expected postgre.port=5432, got %d", got)
	}
}
