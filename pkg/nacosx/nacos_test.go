package nacosx

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/config"
)

type memorySource struct {
	key    string
	format string
	data   []byte
}

func (m *memorySource) Load() ([]*config.KeyValue, error) {
	return []*config.KeyValue{
		{
			Key:    m.key,
			Value:  m.data,
			Format: m.format,
		},
	}, nil
}

func (m *memorySource) Watch() (config.Watcher, error) {
	return &noopWatcher{}, nil
}

type noopWatcher struct{}

func (w *noopWatcher) Next() ([]*config.KeyValue, error) { select {} }
func (w *noopWatcher) Stop() error                      { return nil }

func newTestConfig(t *testing.T, jsonBody string) config.Config {
	t.Helper()
	c := config.New(config.WithSource(&memorySource{
		key:    "test.json",
		format: "json",
		data:   []byte(jsonBody),
	}))
	if err := c.Load(); err != nil {
		t.Fatalf("load test config: %v", err)
	}
	return c
}

func TestBuildClientConfigDefaultsAndEnv(t *testing.T) {
	t.Setenv("NACOS_LOG_LEVEL", "")
	t.Setenv("NACOS_LOG_DIR", "")
	t.Setenv("NACOS_CACHE_DIR", "")
	t.Setenv("NACOS_USERNAME", "")
	t.Setenv("NACOS_PASSWORD", "")

	cc := buildClientConfig("ns-1")
	if cc.LogLevel != "warn" {
		t.Fatalf("expected default LogLevel 'warn', got %q", cc.LogLevel)
	}
	wantLogDir := filepath.Join(os.TempDir(), "nacos", "log")
	if cc.LogDir != wantLogDir {
		t.Fatalf("expected default LogDir %q, got %q", wantLogDir, cc.LogDir)
	}
	wantCacheDir := filepath.Join(os.TempDir(), "nacos", "cache")
	if cc.CacheDir != wantCacheDir {
		t.Fatalf("expected default CacheDir %q, got %q", wantCacheDir, cc.CacheDir)
	}
	if cc.NamespaceId != "ns-1" {
		t.Fatalf("expected NamespaceId 'ns-1', got %q", cc.NamespaceId)
	}

	t.Setenv("NACOS_LOG_LEVEL", "error")
	t.Setenv("NACOS_LOG_DIR", "/var/log/nacos")
	t.Setenv("NACOS_CACHE_DIR", "/var/cache/nacos")
	t.Setenv("NACOS_USERNAME", "nacos-admin")
	t.Setenv("NACOS_PASSWORD", "secret-pass")

	cc2 := buildClientConfig("ns-2")
	if cc2.LogLevel != "error" || cc2.LogDir != "/var/log/nacos" || cc2.CacheDir != "/var/cache/nacos" ||
		cc2.Username != "nacos-admin" || cc2.Password != "secret-pass" || cc2.NamespaceId != "ns-2" {
		t.Fatalf("unexpected custom client config: %+v", cc2)
	}
}

func TestResolveConnectionConfigEnvAndFile(t *testing.T) {
	// Run inside empty temp dir so default relative config.yaml paths don't interfere.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	// Missing config.yaml and empty NACOS_ADDR must error.
	t.Setenv("NACOS_CONFIG_FILE", "")
	t.Setenv("NACOS_ADDR", "")
	t.Setenv("NACOS_PORT", "")
	t.Setenv("NACOS_DATA_ID", "")
	t.Setenv("NACOS_GROUP_ID", "")
	if _, err := resolveConnectionConfig(); err == nil {
		t.Fatalf("expected error when no config.yaml and NACOS_ADDR empty")
	}

	// Env-only with defaults.
	t.Setenv("NACOS_ADDR", "nacos.internal")
	cfg, err := resolveConnectionConfig()
	if err != nil {
		t.Fatalf("resolveConnectionConfig env-only failed: %v", err)
	}
	if cfg.Addr != "nacos.internal" || cfg.Port != 8848 || cfg.DataID != "roaming-world.yaml" || cfg.GroupID != "DEFAULT_GROUP" {
		t.Fatalf("unexpected env-only config: %+v", cfg)
	}

	// Local YAML overridden selectively by env (including explicit empty NACOS_NAMESPACE_ID).
	yamlPath := filepath.Join(tmpDir, "custom.yaml")
	yamlContent := `data:
  nacos:
    addr: "127.0.0.1"
    port: 18848
    namespaceId: "dev-ns"
    dataId: "svc.yaml"
    groupId: "CUSTOM_GROUP"
`
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	t.Setenv("NACOS_CONFIG_FILE", yamlPath)
	t.Setenv("NACOS_ADDR", "")
	t.Setenv("NACOS_PORT", "28848")
	t.Setenv("NACOS_NAMESPACE_ID", "")
	t.Setenv("NACOS_DATA_ID", "override.yaml")
	t.Setenv("NACOS_GROUP_ID", "")

	cfg2, err := resolveConnectionConfig()
	if err != nil {
		t.Fatalf("resolveConnectionConfig with file failed: %v", err)
	}
	if cfg2.Addr != "127.0.0.1" || cfg2.Port != 28848 || cfg2.NamespaceID != "" ||
		cfg2.DataID != "override.yaml" || cfg2.GroupID != "CUSTOM_GROUP" {
		t.Fatalf("unexpected merged config: %+v", cfg2)
	}
}

func TestTypedConfigHelpers(t *testing.T) {
	cfg := newTestConfig(t, `{
		"app": {
			"name": "roaming-world",
			"empty": "",
			"port": 8080,
			"zero": 0,
			"badInt": "not-an-int",
			"ttlStr": "30m",
			"ttlNs": 5000000000,
			"ttlZero": "0s",
			"ttlBad": "invalid"
		}
	}`)

	// GetString & GetStringOrDefault
	if v, err := GetString(cfg, "app.name"); err != nil || v != "roaming-world" {
		t.Fatalf("GetString app.name = (%q, %v)", v, err)
	}
	if _, err := GetString(cfg, "app.missing"); err == nil {
		t.Fatalf("expected error for missing string key")
	}
	if _, err := GetString(nil, "app.name"); err == nil {
		t.Fatalf("expected error for nil config")
	}
	if v := GetStringOrDefault(cfg, "app.name", "fallback"); v != "roaming-world" {
		t.Fatalf("got %q, want 'roaming-world'", v)
	}
	if v := GetStringOrDefault(cfg, "app.empty", "fallback"); v != "fallback" {
		t.Fatalf("got %q, want 'fallback'", v)
	}
	if v := GetStringOrDefault(cfg, "app.missing", "fallback"); v != "fallback" {
		t.Fatalf("got %q, want 'fallback'", v)
	}
	if v := GetStringOrDefault(nil, "app.name", "fallback"); v != "fallback" {
		t.Fatalf("got %q, want 'fallback'", v)
	}

	// GetInt & GetIntOrDefault
	if v, err := GetInt(cfg, "app.port"); err != nil || v != 8080 {
		t.Fatalf("GetInt app.port = (%d, %v)", v, err)
	}
	if v, err := GetInt(cfg, "app.zero"); err != nil || v != 0 {
		t.Fatalf("GetInt app.zero = (%d, %v)", v, err)
	}
	if _, err := GetInt(cfg, "app.badInt"); err == nil {
		t.Fatalf("expected error for badInt")
	}
	if v := GetIntOrDefault(cfg, "app.port", 9000); v != 8080 {
		t.Fatalf("got %d, want 8080", v)
	}
	if v := GetIntOrDefault(cfg, "app.zero", 9000); v != 0 {
		t.Fatalf("got %d, want 0", v)
	}
	if v := GetIntOrDefault(cfg, "app.missing", 9000); v != 9000 {
		t.Fatalf("got %d, want 9000", v)
	}
	if v := GetIntOrDefault(cfg, "app.badInt", 9000); v != 9000 {
		t.Fatalf("got %d, want 9000", v)
	}

	// GetDurationOrDefault
	if d := GetDurationOrDefault(cfg, "app.ttlStr", time.Minute); d != 30*time.Minute {
		t.Fatalf("got %v, want 30m", d)
	}
	if d := GetDurationOrDefault(cfg, "app.ttlNs", time.Minute); d != 5*time.Second {
		t.Fatalf("got %v, want 5s", d)
	}
	if d := GetDurationOrDefault(cfg, "app.ttlZero", time.Minute); d != time.Minute {
		t.Fatalf("got %v, want 1m fallback for zero duration", d)
	}
	if d := GetDurationOrDefault(cfg, "app.ttlBad", time.Minute); d != time.Minute {
		t.Fatalf("got %v, want 1m fallback for bad duration", d)
	}
	if d := GetDurationOrDefault(cfg, "app.missing", time.Minute); d != time.Minute {
		t.Fatalf("got %v, want 1m fallback for missing duration", d)
	}
	if d := GetDurationOrDefault(nil, "app.ttlStr", time.Minute); d != time.Minute {
		t.Fatalf("got %v, want 1m fallback for nil cfg", d)
	}

	// MustGetString & MustGetInt
	if v := MustGetString(cfg, "app.name"); v != "roaming-world" {
		t.Fatalf("MustGetString got %q", v)
	}
	if v := MustGetInt(cfg, "app.port"); v != 8080 {
		t.Fatalf("MustGetInt got %d", v)
	}

	assertPanics(t, func() { _ = MustGetString(cfg, "app.missing") })
	assertPanics(t, func() { _ = MustGetInt(cfg, "app.badInt") })
}

func assertPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic, but function returned normally")
		}
	}()
	fn()
}
