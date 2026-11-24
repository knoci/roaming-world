package conf

import (
	"context"
	"sync"
	"testing"

	"github.com/go-kratos/kratos/v2/config"
)

type staticWatcher struct {
	stop chan struct{}
	once sync.Once
}

func (w *staticWatcher) Next() ([]*config.KeyValue, error) {
	<-w.stop
	return nil, context.Canceled
}

func (w *staticWatcher) Stop() error {
	w.once.Do(func() {
		close(w.stop)
	})
	return nil
}

type staticSource struct {
	kv *config.KeyValue
}

func (s *staticSource) Load() ([]*config.KeyValue, error) {
	return []*config.KeyValue{s.kv}, nil
}

func (s *staticSource) Watch() (config.Watcher, error) {
	return &staticWatcher{stop: make(chan struct{})}, nil
}

func TestConfigGettersDelegateToNacosx(t *testing.T) {
	cfg := config.New(config.WithSource(&staticSource{
		kv: &config.KeyValue{
			Key:    "test.yaml",
			Format: "yaml",
			Value:  []byte("postgre:\n  host: 127.0.0.1\n  port: 5432\n"),
		},
	}))
	if err := cfg.Load(); err != nil {
		t.Fatalf("load static config: %v", err)
	}
	defer cfg.Close()

	if got := GetConfigString(cfg, "postgre.host"); got != "127.0.0.1" {
		t.Fatalf("expected postgre.host=127.0.0.1, got %q", got)
	}
	if got := GetConfigInt(cfg, "postgre.port"); got != 5432 {
		t.Fatalf("expected postgre.port=5432, got %d", got)
	}

	t.Run("missing string key panics via nacosx.MustGetString", func(t *testing.T) {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("expected panic for missing required string key")
			}
		}()
		_ = GetConfigString(cfg, "missing.key")
	})

	t.Run("missing int key panics via nacosx.MustGetInt", func(t *testing.T) {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("expected panic for missing required int key")
			}
		}()
		_ = GetConfigInt(cfg, "missing.port")
	})
}
