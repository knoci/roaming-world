package pkg

import (
	"context"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
)

type Handler func(context.Context, []byte) error

type KafkaReceiver struct {
	reader *kafka.Reader
	cancel context.CancelFunc
	done   chan struct{}
}

func NewKafkaReceiver(address []string, topic, groupID string) (*KafkaReceiver, error) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  address,
		Topic:    topic,
		GroupID:  groupID,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		MaxWait:  time.Second,
	})
	return &KafkaReceiver{reader: r}, nil
}

func (k *KafkaReceiver) Receive(ctx context.Context, handler Handler) {
	ctx, k.cancel = context.WithCancel(ctx)
	k.done = make(chan struct{})
	go func() {
		defer close(k.done)
		consume(ctx, k.reader, handler)
	}()
}

type messageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
}

func consume(ctx context.Context, reader messageReader, handler Handler) {
	for ctx.Err() == nil {
		var event kafka.Message
		if !retryMessageOperation(ctx, "fetch", func() error {
			var err error
			event, err = reader.FetchMessage(ctx)
			return err
		}) {
			return
		}
		if !retryMessageOperation(ctx, "handle", func() error {
			return handler(ctx, event.Value)
		}) {
			return
		}
		if !retryMessageOperation(ctx, "commit", func() error {
			return reader.CommitMessages(ctx, event)
		}) {
			return
		}
	}
}

func retryMessageOperation(ctx context.Context, operation string, run func() error) bool {
	for ctx.Err() == nil {
		if err := run(); err != nil {
			if ctx.Err() != nil {
				return false
			}
			log.Printf("comment event %s failed, retrying: %v", operation, err)
		} else {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	return false
}

func (k *KafkaReceiver) Close() error {
	if k.cancel != nil {
		k.cancel()
		<-k.done
	}
	return k.reader.Close()
}
