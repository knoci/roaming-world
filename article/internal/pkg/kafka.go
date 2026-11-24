package pkg

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	defaultMaxRetries     = 5
	defaultRetryBaseDelay = 500 * time.Millisecond
	defaultRetryMaxDelay  = 10 * time.Second
	defaultHandleTimeout  = 10 * time.Second

	dlqHeaderReason        = "x-dlq-reason"
	dlqHeaderOriginalTopic = "x-dlq-original-topic"
	dlqHeaderFailedAt      = "x-dlq-failed-at"
)

type Handler func(context.Context, []byte) error

type dlqPublisher interface {
	Publish(ctx context.Context, msg kafka.Message, reason string) error
}

type messageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
}

type messageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

type consumeOptions struct {
	maxRetries    int
	baseDelay     time.Duration
	maxDelay      time.Duration
	handleTimeout time.Duration
	dlq           dlqPublisher
	sleep         func(ctx context.Context, d time.Duration) bool
}

func defaultConsumeOptions() consumeOptions {
	return consumeOptions{
		maxRetries:    defaultMaxRetries,
		baseDelay:     defaultRetryBaseDelay,
		maxDelay:      defaultRetryMaxDelay,
		handleTimeout: defaultHandleTimeout,
	}
}

func (o consumeOptions) wait(ctx context.Context, d time.Duration) bool {
	if o.sleep != nil {
		return o.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// ParseBrokers parses a comma-separated list of Kafka broker addresses,
// trimming whitespace and discarding empty entries.
func ParseBrokers(address string) []string {
	parts := strings.Split(address, ",")
	brokers := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			brokers = append(brokers, trimmed)
		}
	}
	return brokers
}

func normalizeBrokers(addresses []string) []string {
	brokers := make([]string, 0, len(addresses))
	for _, addr := range addresses {
		brokers = append(brokers, ParseBrokers(addr)...)
	}
	return brokers
}

type kafkaDLQPublisher struct {
	writer        messageWriter
	originalTopic string
	dlqTopic      string
	now           func() time.Time
}

func newKafkaDLQPublisher(brokers []string, originalTopic string) *kafkaDLQPublisher {
	dlqTopic := originalTopic + "-dlq"
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        dlqTopic,
		Balancer:     &kafka.LeastBytes{},
		RequiredAcks: kafka.RequireOne,
		BatchTimeout: 10 * time.Millisecond,
	}
	return &kafkaDLQPublisher{
		writer:        w,
		originalTopic: originalTopic,
		dlqTopic:      dlqTopic,
		now:           time.Now,
	}
}

func (p *kafkaDLQPublisher) buildDLQMessage(msg kafka.Message, reason string) kafka.Message {
	origTopic := msg.Topic
	if origTopic == "" {
		origTopic = p.originalTopic
	}
	nowFn := p.now
	if nowFn == nil {
		nowFn = time.Now
	}

	headers := make([]kafka.Header, 0, len(msg.Headers)+3)
	headers = append(headers, msg.Headers...)
	headers = append(headers,
		kafka.Header{Key: dlqHeaderReason, Value: []byte(reason)},
		kafka.Header{Key: dlqHeaderOriginalTopic, Value: []byte(origTopic)},
		kafka.Header{Key: dlqHeaderFailedAt, Value: []byte(nowFn().UTC().Format(time.RFC3339Nano))},
	)

	return kafka.Message{
		Key:     msg.Key,
		Value:   msg.Value,
		Headers: headers,
	}
}

func (p *kafkaDLQPublisher) Publish(ctx context.Context, msg kafka.Message, reason string) error {
	if p == nil || p.writer == nil {
		return errors.New("kafka: dlq writer is nil")
	}
	return p.writer.WriteMessages(ctx, p.buildDLQMessage(msg, reason))
}

func (p *kafkaDLQPublisher) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}

type KafkaReceiver struct {
	reader *kafka.Reader
	dlq    *kafkaDLQPublisher
	opts   consumeOptions
	cancel context.CancelFunc
	done   chan struct{}
}

func NewKafkaReceiver(address []string, topic, groupID string) (*KafkaReceiver, error) {
	brokers := normalizeBrokers(address)
	if len(brokers) == 0 {
		return nil, errors.New("kafka: at least one broker address is required")
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, errors.New("kafka: topic is required")
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		Topic:    topic,
		GroupID:  groupID,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		MaxWait:  time.Second,
	})
	dlq := newKafkaDLQPublisher(brokers, topic)
	opts := defaultConsumeOptions()
	opts.dlq = dlq
	return &KafkaReceiver{
		reader: r,
		dlq:    dlq,
		opts:   opts,
	}, nil
}

func (k *KafkaReceiver) Receive(ctx context.Context, handler Handler) {
	ctx, k.cancel = context.WithCancel(ctx)
	k.done = make(chan struct{})
	go func() {
		defer close(k.done)
		consumeWithOptions(ctx, k.reader, handler, k.opts)
	}()
}

func consume(ctx context.Context, reader messageReader, handler Handler) {
	consumeWithOptions(ctx, reader, handler, defaultConsumeOptions())
}

func consumeWithOptions(ctx context.Context, reader messageReader, handler Handler, opts consumeOptions) {
	for ctx.Err() == nil {
		var event kafka.Message
		if !retryMessageOperation(ctx, "fetch", opts, func() error {
			var err error
			event, err = reader.FetchMessage(ctx)
			return err
		}) {
			return
		}
		if !handleWithRetry(ctx, event, handler, opts) {
			return
		}
		if !retryMessageOperation(ctx, "commit", opts, func() error {
			return reader.CommitMessages(ctx, event)
		}) {
			return
		}
	}
}

func handleWithRetry(ctx context.Context, event kafka.Message, handler Handler, opts consumeOptions) bool {
	maxRetries := opts.maxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	handleTimeout := opts.handleTimeout
	if handleTimeout <= 0 {
		handleTimeout = defaultHandleTimeout
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		attemptCtx, cancel := context.WithTimeout(ctx, handleTimeout)
		err := handler(attemptCtx, event.Value)
		cancel()
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		lastErr = err
		log.Printf("comment event handle failed (attempt %d/%d, topic=%s, offset=%d): %v",
			attempt+1, maxRetries, event.Topic, event.Offset, err)

		if attempt+1 < maxRetries {
			delay := backoffDelay(attempt, opts.baseDelay, opts.maxDelay)
			if !opts.wait(ctx, delay) {
				return false
			}
		}
	}

	reason := "handler failed"
	if lastErr != nil {
		reason = lastErr.Error()
	}
	if opts.dlq != nil {
		dlqCtx, cancel := context.WithTimeout(ctx, handleTimeout)
		if dlqErr := opts.dlq.Publish(dlqCtx, event, reason); dlqErr != nil {
			log.Printf("comment event DLQ publish failed (topic=%s, offset=%d): %v", event.Topic, event.Offset, dlqErr)
		}
		cancel()
	} else {
		log.Printf("comment event exhausted %d attempts without DLQ publisher (topic=%s, offset=%d): %s",
			maxRetries, event.Topic, event.Offset, reason)
	}
	return ctx.Err() == nil
}

func retryMessageOperation(ctx context.Context, operation string, opts consumeOptions, run func() error) bool {
	for attempt := 0; ctx.Err() == nil; attempt++ {
		if err := run(); err != nil {
			if ctx.Err() != nil {
				return false
			}
			log.Printf("comment event %s failed, retrying: %v", operation, err)
		} else {
			return true
		}
		delay := backoffDelay(attempt, opts.baseDelay, opts.maxDelay)
		if !opts.wait(ctx, delay) {
			return false
		}
	}
	return false
}

func backoffDelay(attempt int, base, maxDelay time.Duration) time.Duration {
	if base <= 0 {
		base = defaultRetryBaseDelay
	}
	if maxDelay <= 0 {
		maxDelay = defaultRetryMaxDelay
	}
	if base > maxDelay {
		return maxDelay
	}
	delay := base
	for i := 0; i < attempt; i++ {
		if delay >= maxDelay/2 {
			return maxDelay
		}
		delay *= 2
	}
	return delay
}

func (k *KafkaReceiver) Close() error {
	if k.cancel != nil {
		k.cancel()
		<-k.done
	}
	var errs []error
	if k.reader != nil {
		errs = append(errs, k.reader.Close())
	}
	if k.dlq != nil {
		errs = append(errs, k.dlq.Close())
	}
	return errors.Join(errs...)
}
