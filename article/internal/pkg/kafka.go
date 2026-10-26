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

func (m *Message) Key() string {
	return m.key
}

func (m *Message) Value() []byte {
	return m.value
}

func NewMessage(key string, value []byte) *Message {
	return &Message{
		key:   key,
		value: value,
	}
}

type KafkaSender struct {
	writer *kafka.Writer
	topic  string
}

type KafkaReceiver struct {
	reader *kafka.Reader
	topic  string
}

func (s *KafkaSender) Send(ctx context.Context, message *Message) error {
	return s.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(message.Key()),
		Value: message.Value(),
	})
}

func (s *KafkaSender) SendBatch(ctx context.Context, messages []*Message) error {
	kafkaMessages := make([]kafka.Message, len(messages))
	for i, msg := range messages {
		kafkaMessages[i] = kafka.Message{
			Key:   []byte(msg.Key()),
			Value: msg.Value(),
		}
	}
	return s.writer.WriteMessages(ctx, kafkaMessages...)
}

func (s *KafkaSender) Close() error {
	return s.writer.Close()
}

func NewKafkaSender(address []string, topic string) (*KafkaSender, error) {
	w := &kafka.Writer{
		Topic:        topic,
		Addr:         kafka.TCP(address...),
		Balancer:     &kafka.LeastBytes{},
		BatchSize:    100,
		BatchTimeout: 80 * time.Millisecond,
		Async:        true,
		Compression:  kafka.Snappy,
	}
	return &KafkaSender{writer: w, topic: topic}, nil
}

func (k *KafkaReceiver) Receive(ctx context.Context, handler Handler) error {
	go func() {
		for {
			m, err := k.reader.FetchMessage(context.Background())
			if err != nil {
				break
			}
			err = handler(context.Background(), &Message{
				key:   string(m.Key),
				value: m.Value,
			})
			if err != nil {
				log.Fatal("message handling exception:", err)
			}
			if err := k.reader.CommitMessages(ctx, m); err != nil {
				log.Fatal("failed to commit messages:", err)
			}
		}
	}()
	return nil
}

func (k *KafkaReceiver) Close() error {
	return k.reader.Close()
}
