package conf

import (
	"testing"

	"github.com/go-kratos/kratos/v2/config"
)

type memorySource struct {
	data []byte
}

func (m *memorySource) Load() ([]*config.KeyValue, error) {
	return []*config.KeyValue{{Key: "test.json", Format: "json", Value: m.data}}, nil
}

func (m *memorySource) Watch() (config.Watcher, error) {
	return &noopWatcher{}, nil
}

type noopWatcher struct{}

func (w *noopWatcher) Next() ([]*config.KeyValue, error) { select {} }
func (w *noopWatcher) Stop() error                      { return nil }

func TestNacosConfigDelegation(t *testing.T) {
	cfg := config.New(config.WithSource(&memorySource{
		data: []byte(`{"postgre":{"host":"127.0.0.1","port":5432}}`),
	}))
	if err := cfg.Load(); err != nil {
		t.Fatalf("load test config: %v", err)
	}

	if got := GetConfigString(cfg, "postgre.host"); got != "127.0.0.1" {
		t.Fatalf("expected 127.0.0.1, got %q", got)
	}
	if got := GetConfigInt(cfg, "postgre.port"); got != 5432 {
		t.Fatalf("expected 5432, got %d", got)
	}

	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic for missing key")
		}
	}()
	_ = GetConfigString(cfg, "postgre.missing")
}
