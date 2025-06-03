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
	err := s.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(message.Key()),
		Value: message.Value(),
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *KafkaSender) SendBatch(ctx context.Context, messages []*Message) error {
	kafkaMessages := make([]kafka.Message, len(messages))
	for i, msg := range messages {
		kafkaMessages[i] = kafka.Message{
			Key:   []byte(msg.Key()),
			Value: msg.Value(),
		}
	}

	err := s.writer.WriteMessages(ctx, kafkaMessages...)
	if err != nil {
		return err
	}
	return nil
}

func (s *KafkaSender) Close() error {
	err := s.writer.Close()
	if err != nil {
		return err
	}
	return nil
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

func (k *KafkaReceiver) ReceiveParallel(ctx context.Context, handler Handler, workerCount int) error {
	// 创建一个用于接收消息的通道
	messageChan := make(chan kafka.Message, 100)

	// 启动一个goroutine来获取消息并发送到通道
	go func() {
		for {
			m, err := k.reader.FetchMessage(ctx)
			if err != nil {
				log.Printf("Error fetching message: %v", err)
				close(messageChan)
				break
			}
			messageChan <- m
		}
	}()

	// 启动多个worker goroutines来处理消息
	for i := 0; i < workerCount; i++ {
		go func(workerID int) {
			for m := range messageChan {
				err := handler(ctx, &Message{
					key:   string(m.Key),
					value: m.Value,
				})
				if err != nil {
					log.Printf("Worker %d: message handling exception: %v", workerID, err)
					continue
				}

				if err := k.reader.CommitMessages(ctx, m); err != nil {
					log.Printf("Worker %d: failed to commit messages: %v", workerID, err)
				}
			}
		}(i)
	}

	return nil
}

func (k *KafkaReceiver) Close() error {
	err := k.reader.Close()
	if err != nil {
		return err
	}
	return nil
}

func NewKafkaReceiver(address []string, topic string) (*KafkaReceiver, error) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:          address,
		GroupID:          "group-a",
		Topic:            topic,
		MinBytes:         10e3, // 10KB
		MaxBytes:         10e6, // 10MB
		MaxWait:          200 * time.Millisecond,
		CommitInterval:   time.Second,
		ReadBatchTimeout: 300 * time.Millisecond,
		MaxAttempts:      4,
	})
	return &KafkaReceiver{reader: r, topic: topic}, nil
}
