package pkg

import (
	"context"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
)

type Handler func(context.Context, *Message) error

type Message struct {
	key   string
	value []byte
}

func (m *Message) Key() string   { return m.key }
func (m *Message) Value() []byte { return m.value }

type KafkaReceiver struct {
	reader *kafka.Reader
	topic  string
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
	return &KafkaReceiver{reader: r, topic: topic}, nil
}

func (k *KafkaReceiver) Receive(ctx context.Context, handler Handler) error {
	go func() {
		for {
			m, err := k.reader.FetchMessage(context.Background())
			if err != nil {
				break
			}
			if err := handler(context.Background(), &Message{key: string(m.Key), value: m.Value}); err != nil {
				log.Printf("message handling exception: %v", err)
				continue
			}
			if err := k.reader.CommitMessages(ctx, m); err != nil {
				log.Printf("failed to commit messages: %v", err)
			}
		}
	}()
	return nil
}

func (k *KafkaReceiver) Close() error { return k.reader.Close() }
