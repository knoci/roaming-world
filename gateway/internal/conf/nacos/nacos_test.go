package conf

import (
	"testing"

	"github.com/go-kratos/kratos/v2/config"
)

type memorySource struct {
	data []byte
}

func (m *memorySource) Load() ([]*config.KeyValue, error) {
	return []*config.KeyValue{
		{
			Key:    "test.json",
			Value:  m.data,
			Format: "json",
		},
	}, nil
}

func (m *memorySource) Watch() (config.Watcher, error) {
	return &noopWatcher{}, nil
}

type noopWatcher struct{}

func (w *noopWatcher) Next() ([]*config.KeyValue, error) { select {} }
func (w *noopWatcher) Stop() error                      { return nil }

func TestGetConfigDelegatesToNacosx(t *testing.T) {
	c := config.New(config.WithSource(&memorySource{
		data: []byte(`{"service":{"name":"gateway","port":8000}}`),
	}))
	if err := c.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if got := GetConfigString(c, "service.name"); got != "gateway" {
		t.Fatalf("expected service.name 'gateway', got %q", got)
	}
	if got := GetConfigInt(c, "service.port"); got != 8000 {
		t.Fatalf("expected service.port 8000, got %d", got)
	}
}
