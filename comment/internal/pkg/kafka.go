package pkg

import (
	"context"
	"errors"
	"strings"

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

// ParseBrokers splits a comma-separated Kafka broker address string into trimmed, non-empty endpoints.
func ParseBrokers(addr string) []string {
	return parseBrokers(addr)
}

func parseBrokers(addr string) []string {
	parts := strings.Split(addr, ",")
	brokers := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			brokers = append(brokers, trimmed)
		}
	}
	return brokers
}

// NewKafkaSyncSender 用于 outbox：必须等 broker 确认后才能标记 published。
// 使用 Hash Balancer 保证相同 EventKey (aid) 始终路由到同一分区有序投递。
func NewKafkaSyncSender(address []string, topic string) (*KafkaSender, error) {
	brokers := make([]string, 0, len(address))
	for _, raw := range address {
		brokers = append(brokers, parseBrokers(raw)...)
	}
	if len(brokers) == 0 {
		return nil, errors.New("kafka: at least one broker address is required")
	}
	w := &kafka.Writer{
		Topic:        topic,
		Addr:         kafka.TCP(brokers...),
		Balancer:     &kafka.Hash{},
		BatchSize:    1,
		Async:        false,
		Compression:  kafka.Snappy,
		RequiredAcks: kafka.RequireAll,
	}
	return &KafkaSender{writer: w, topic: topic}, nil
}
