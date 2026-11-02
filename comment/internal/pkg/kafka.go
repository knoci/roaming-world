package pkg

import (
	"context"

	"github.com/segmentio/kafka-go"
)

type Message struct {
	key   string
	value []byte
}

func (m *Message) Key() string   { return m.key }
func (m *Message) Value() []byte { return m.value }

func NewMessage(key string, value []byte) *Message {
	return &Message{key: key, value: value}
}

type KafkaSender struct {
	writer *kafka.Writer
	topic  string
}

func (s *KafkaSender) Send(ctx context.Context, message *Message) error {
	return s.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(message.Key()),
		Value: message.Value(),
	})
}

func (s *KafkaSender) Close() error { return s.writer.Close() }

// NewKafkaSyncSender 用于 outbox：必须等 broker 确认后才能标记 published。
func NewKafkaSyncSender(address []string, topic string) (*KafkaSender, error) {
	w := &kafka.Writer{
		Topic:        topic,
		Addr:         kafka.TCP(address...),
		Balancer:     &kafka.LeastBytes{},
		BatchSize:    1,
		Async:        false,
		Compression:  kafka.Snappy,
		RequiredAcks: kafka.RequireAll,
	}
	return &KafkaSender{writer: w, topic: topic}, nil
}
