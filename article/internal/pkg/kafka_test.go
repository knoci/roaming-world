package pkg

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

type scriptedCommentReader struct {
	trace       []string
	offset      int64
	firstCommit bool
	cancel      context.CancelFunc
}

func (r *scriptedCommentReader) FetchMessage(context.Context) (kafka.Message, error) {
	r.offset++
	id := strconv.FormatInt(r.offset, 10)
	r.trace = append(r.trace, "fetch "+id)
	return kafka.Message{Offset: r.offset, Value: []byte(id)}, nil
}

func (r *scriptedCommentReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	offset := messages[0].Offset
	r.trace = append(r.trace, "commit "+strconv.FormatInt(offset, 10))
	if offset == 1 && !r.firstCommit {
		r.firstCommit = true
		return errors.New("temporary commit failure")
	}
	if offset == 2 {
		r.cancel()
	}
	return nil
}

func TestConsumerRetriesBeforeAdvancingOffset(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	reader := &scriptedCommentReader{cancel: cancel}
	handledFirst := false
	consume(ctx, reader, func(_ context.Context, payload []byte) error {
		id := string(payload)
		reader.trace = append(reader.trace, "handle "+id)
		if id == "1" && !handledFirst {
			handledFirst = true
			return errors.New("temporary database failure")
		}
		return nil
	})
	expected := []string{"fetch 1", "handle 1", "handle 1", "commit 1", "commit 1", "fetch 2", "handle 2", "commit 2"}
	if !slices.Equal(expected, reader.trace) {
		t.Fatalf("unexpected consumer order: %v", reader.trace)
	}
}

type queueReader struct {
	mu        sync.Mutex
	messages  []kafka.Message
	idx       int
	committed []int64
	cancel    context.CancelFunc
}

func (r *queueReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idx >= len(r.messages) {
		r.cancel()
		return kafka.Message{}, context.Canceled
	}
	msg := r.messages[r.idx]
	r.idx++
	return msg, nil
}

func (r *queueReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range messages {
		r.committed = append(r.committed, m.Offset)
	}
	if len(r.committed) >= len(r.messages) {
		r.cancel()
	}
	return nil
}

type recordedDLQCall struct {
	msg    kafka.Message
	reason string
}

type stubDLQ struct {
	mu    sync.Mutex
	calls []recordedDLQCall
	err   error
}

func (s *stubDLQ) Publish(_ context.Context, msg kafka.Message, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, recordedDLQCall{msg: msg, reason: reason})
	return s.err
}

type stubMessageWriter struct {
	mu       sync.Mutex
	messages []kafka.Message
	closed   bool
}

func (w *stubMessageWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messages = append(w.messages, msgs...)
	return nil
}

func (w *stubMessageWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return nil
}

func TestConsumerBoundedRetriesPublishesToDLQAndCommitsOffset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reader := &queueReader{
		cancel: cancel,
		messages: []kafka.Message{
			{Topic: "comment-event", Offset: 10, Key: []byte("k1"), Value: []byte("poison")},
			{Topic: "comment-event", Offset: 11, Key: []byte("k2"), Value: []byte("healthy")},
		},
	}
	dlq := &stubDLQ{}
	var sleeps []time.Duration
	opts := consumeOptions{
		maxRetries:    5,
		baseDelay:     500 * time.Millisecond,
		maxDelay:      10 * time.Second,
		handleTimeout: time.Second,
		dlq:           dlq,
		sleep: func(_ context.Context, d time.Duration) bool {
			sleeps = append(sleeps, d)
			return true
		},
	}

	attempts := map[string]int{}
	consumeWithOptions(ctx, reader, func(_ context.Context, payload []byte) error {
		val := string(payload)
		attempts[val]++
		if val == "poison" {
			return errors.New("permanent db constraint error")
		}
		return nil
	}, opts)

	if attempts["poison"] != 5 {
		t.Fatalf("expected 5 attempts for poison message, got %d", attempts["poison"])
	}
	if attempts["healthy"] != 1 {
		t.Fatalf("expected healthy message to be processed after poison message, got %d attempts", attempts["healthy"])
	}
	if len(dlq.calls) != 1 {
		t.Fatalf("expected 1 DLQ publish, got %d", len(dlq.calls))
	}
	if string(dlq.calls[0].msg.Value) != "poison" || dlq.calls[0].reason != "permanent db constraint error" {
		t.Fatalf("unexpected DLQ call: %+v", dlq.calls[0])
	}
	wantCommitted := []int64{10, 11}
	if !slices.Equal(wantCommitted, reader.committed) {
		t.Fatalf("expected offsets %v committed, got %v", wantCommitted, reader.committed)
	}
	wantSleeps := []time.Duration{
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
	}
	if !slices.Equal(wantSleeps, sleeps) {
		t.Fatalf("expected exponential backoff %v, got %v", wantSleeps, sleeps)
	}
}

func TestConsumerCommitsOffsetEvenWhenDLQUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reader := &queueReader{
		cancel: cancel,
		messages: []kafka.Message{
			{Topic: "comment-event", Offset: 1, Value: []byte("poison")},
			{Topic: "comment-event", Offset: 2, Value: []byte("ok")},
		},
	}
	dlq := &stubDLQ{err: errors.New("dlq broker down")}
	opts := consumeOptions{
		maxRetries:    2,
		baseDelay:     time.Millisecond,
		maxDelay:      10 * time.Millisecond,
		handleTimeout: time.Second,
		dlq:           dlq,
		sleep:         func(context.Context, time.Duration) bool { return true },
	}

	consumeWithOptions(ctx, reader, func(_ context.Context, payload []byte) error {
		if string(payload) == "poison" {
			return errors.New("bad event")
		}
		return nil
	}, opts)

	if len(dlq.calls) != 1 {
		t.Fatalf("expected 1 DLQ publish attempt, got %d", len(dlq.calls))
	}
	wantCommitted := []int64{1, 2}
	if !slices.Equal(wantCommitted, reader.committed) {
		t.Fatalf("partition stalled when DLQ failed: committed=%v, want %v", reader.committed, wantCommitted)
	}
}

func TestConsumerPerAttemptTimeoutTriggersRetryAndDLQ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reader := &queueReader{
		cancel: cancel,
		messages: []kafka.Message{
			{Topic: "comment-event", Offset: 42, Value: []byte("slow")},
		},
	}
	dlq := &stubDLQ{}
	opts := consumeOptions{
		maxRetries:    2,
		baseDelay:     5 * time.Millisecond,
		maxDelay:      20 * time.Millisecond,
		handleTimeout: 30 * time.Millisecond,
		dlq:           dlq,
	}

	var count int
	consumeWithOptions(ctx, reader, func(attemptCtx context.Context, _ []byte) error {
		count++
		<-attemptCtx.Done()
		return attemptCtx.Err()
	}, opts)

	if count != 2 {
		t.Fatalf("expected 2 timed-out attempts, got %d", count)
	}
	if len(dlq.calls) != 1 {
		t.Fatalf("expected 1 DLQ publish after timeout exhaustion, got %d", len(dlq.calls))
	}
	if !strings.Contains(dlq.calls[0].reason, context.DeadlineExceeded.Error()) {
		t.Fatalf("expected deadline exceeded reason in DLQ, got %q", dlq.calls[0].reason)
	}
	if !slices.Equal([]int64{42}, reader.committed) {
		t.Fatalf("expected offset 42 committed after timeout DLQ, got %v", reader.committed)
	}
}

func TestBackoffDelayExponentialAndCapped(t *testing.T) {
	base := 500 * time.Millisecond
	maxDelay := 10 * time.Second
	want := []time.Duration{
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		10 * time.Second,
		10 * time.Second,
	}
	for i, w := range want {
		if got := backoffDelay(i, base, maxDelay); got != w {
			t.Fatalf("attempt %d: got %v, want %v", i, got, w)
		}
	}
	// Large attempt index must never overflow.
	if got := backoffDelay(60, base, maxDelay); got != maxDelay {
		t.Fatalf("attempt 60: got %v, want %v", got, maxDelay)
	}
}

func TestKafkaDLQPublisherHeadersAndTopic(t *testing.T) {
	writer := &stubMessageWriter{}
	fixedTime := time.Date(2026, 11, 22, 12, 34, 56, 0, time.UTC)
	pub := &kafkaDLQPublisher{
		writer:        writer,
		originalTopic: "comment-event",
		dlqTopic:      "comment-event-dlq",
		now:           func() time.Time { return fixedTime },
	}

	orig := kafka.Message{
		Key:     []byte("aid-1"),
		Value:   []byte(`{"aid":"aid-1"}`),
		Headers: []kafka.Header{{Key: "x-trace-id", Value: []byte("trace-1")}},
	}
	if err := pub.Publish(context.Background(), orig, "boom"); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	if len(writer.messages) != 1 {
		t.Fatalf("expected 1 DLQ message written, got %d", len(writer.messages))
	}
	got := writer.messages[0]
	if string(got.Key) != "aid-1" || string(got.Value) != `{"aid":"aid-1"}` {
		t.Fatalf("unexpected DLQ payload/key: %+v", got)
	}
	headers := make(map[string]string, len(got.Headers))
	for _, h := range got.Headers {
		headers[h.Key] = string(h.Value)
	}
	if headers["x-trace-id"] != "trace-1" {
		t.Fatalf("expected original header preserved, got %+v", headers)
	}
	if headers[dlqHeaderReason] != "boom" {
		t.Fatalf("expected %s=boom, got %q", dlqHeaderReason, headers[dlqHeaderReason])
	}
	if headers[dlqHeaderOriginalTopic] != "comment-event" {
		t.Fatalf("expected %s=comment-event, got %q", dlqHeaderOriginalTopic, headers[dlqHeaderOriginalTopic])
	}
	if headers[dlqHeaderFailedAt] != fixedTime.Format(time.RFC3339Nano) {
		t.Fatalf("expected %s=%s, got %q", dlqHeaderFailedAt, fixedTime.Format(time.RFC3339Nano), headers[dlqHeaderFailedAt])
	}

	// Verify real constructor targets <topic>-dlq.
	recv, err := NewKafkaReceiver([]string{"b1:9092, b2:9092"}, "comment-event", "grp-1")
	if err != nil {
		t.Fatalf("NewKafkaReceiver failed: %v", err)
	}
	defer func() { _ = recv.Close() }()
	if recv.dlq == nil || recv.dlq.dlqTopic != "comment-event-dlq" {
		t.Fatalf("expected DLQ topic comment-event-dlq, got %+v", recv.dlq)
	}
	if len(recv.reader.Config().Brokers) != 2 {
		t.Fatalf("expected 2 brokers in reader config, got %v", recv.reader.Config().Brokers)
	}
}

func TestParseBrokers(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{in: "", want: []string{}},
		{in: "   ,  ", want: []string{}},
		{in: "kafka:9092", want: []string{"kafka:9092"}},
		{in: "kafka-1:9092, kafka-2:9092 , ,kafka-3:9092 ", want: []string{"kafka-1:9092", "kafka-2:9092", "kafka-3:9092"}},
	}
	for _, tc := range cases {
		got := ParseBrokers(tc.in)
		if !slices.Equal(got, tc.want) {
			t.Fatalf("ParseBrokers(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
