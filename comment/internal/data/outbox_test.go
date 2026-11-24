package data

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/knoci/roaming-world/comment/internal/pkg"
)

type fakeOutboxStore struct {
	mu            sync.Mutex
	inTX          atomic.Bool
	claimCalls    int
	lastLimit     int
	lastLease     time.Duration
	batches       [][]CommentOutbox
	claimErr      error
	applyErr      error
	applied       [][]outboxOutcome
	eventsLog     *[]string
	eventsMu      *sync.Mutex
	onApplyCalled func()
}

func (s *fakeOutboxStore) record(ev string) {
	if s.eventsLog == nil || s.eventsMu == nil {
		return
	}
	s.eventsMu.Lock()
	*s.eventsLog = append(*s.eventsLog, ev)
	s.eventsMu.Unlock()
}

func (s *fakeOutboxStore) Claim(_ context.Context, limit int, lease time.Duration) ([]CommentOutbox, error) {
	s.inTX.Store(true)
	defer s.inTX.Store(false)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimCalls++
	s.lastLimit = limit
	s.lastLease = lease
	s.record("claim_tx")
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if len(s.batches) == 0 {
		return nil, nil
	}
	batch := s.batches[0]
	s.batches = s.batches[1:]
	return batch, nil
}

func (s *fakeOutboxStore) ApplyOutcomes(_ context.Context, outcomes []outboxOutcome) error {
	s.inTX.Store(true)
	defer s.inTX.Store(false)

	s.mu.Lock()
	cp := append([]outboxOutcome(nil), outcomes...)
	s.applied = append(s.applied, cp)
	err := s.applyErr
	cb := s.onApplyCalled
	s.mu.Unlock()

	s.record("apply_tx")
	if cb != nil {
		cb()
	}
	return err
}

type fakeEventPublisher struct {
	mu        sync.Mutex
	sendFn    func(ctx context.Context, msg *pkg.Message) error
	sent      []*pkg.Message
	closed    bool
	eventsLog *[]string
	eventsMu  *sync.Mutex
}

func (p *fakeEventPublisher) record(ev string) {
	if p.eventsLog == nil || p.eventsMu == nil {
		return
	}
	p.eventsMu.Lock()
	*p.eventsLog = append(*p.eventsLog, ev)
	p.eventsMu.Unlock()
}

func (p *fakeEventPublisher) Send(ctx context.Context, message *pkg.Message) error {
	p.record("kafka_send")
	p.mu.Lock()
	p.sent = append(p.sent, message)
	fn := p.sendFn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, message)
	}
	return nil
}

func (p *fakeEventPublisher) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.record("kafka_close")
	return nil
}

func TestOutboxDecoupledClaimPublishApply(t *testing.T) {
	var order []string
	var orderMu sync.Mutex

	store := &fakeOutboxStore{
		batches: [][]CommentOutbox{
			{
				{ID: 1, EventID: "e1", EventKey: "aid-1", Payload: []byte(`{"aid":"aid-1","count":1}`), Status: outboxStatusPending},
				{ID: 2, EventID: "e2", EventKey: "aid-2", Payload: []byte(`{"aid":"aid-2","count":2}`), Status: outboxStatusPending},
			},
		},
		eventsLog: &order,
		eventsMu:  &orderMu,
	}

	pub := &fakeEventPublisher{
		eventsLog: &order,
		eventsMu:  &orderMu,
		sendFn: func(ctx context.Context, msg *pkg.Message) error {
			if store.inTX.Load() {
				t.Error("Kafka Send MUST NOT execute inside a DB transaction")
			}
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Error("expected per-message timeout deadline on Kafka Send ctx")
			} else if rem := time.Until(deadline); rem <= 0 || rem > outboxPublishTimeout+time.Second {
				t.Errorf("unexpected Send deadline remaining: %v", rem)
			}
			return nil
		},
	}

	d := &Data{
		event:       pub,
		outboxStore: store,
		eventTopic:  "comment-event",
		log:         log.NewHelper(log.NewStdLogger(io.Discard)),
	}

	if err := d.publishOutbox(context.Background()); err != nil {
		t.Fatalf("publishOutbox failed: %v", err)
	}

	if store.lastLimit != outboxBatchSize {
		t.Fatalf("expected Claim limit=%d, got %d", outboxBatchSize, store.lastLimit)
	}
	if store.lastLease != outboxLeaseDuration {
		t.Fatalf("expected Claim lease=%v, got %v", outboxLeaseDuration, store.lastLease)
	}

	wantOrder := []string{"claim_tx", "kafka_send", "kafka_send", "apply_tx"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("unexpected execution order: got %v, want %v", order, wantOrder)
	}

	if len(store.applied) != 1 || len(store.applied[0]) != 2 {
		t.Fatalf("expected 1 ApplyOutcomes call with 2 outcomes, got %+v", store.applied)
	}
	for i, out := range store.applied[0] {
		if !out.Published || out.Status != outboxStatusPublished || out.PublishedAt.IsZero() {
			t.Fatalf("outcome[%d] expected published with timestamp, got %+v", i, out)
		}
	}
}

func TestOutboxExponentialBackoffAndDeadLetter(t *testing.T) {
	// Verify backoff curve: min(2^retry, 64)s
	wantBackoffs := map[int]time.Duration{
		1:  2 * time.Second,
		2:  4 * time.Second,
		3:  8 * time.Second,
		4:  16 * time.Second,
		5:  32 * time.Second,
		6:  64 * time.Second,
		7:  64 * time.Second,
		9:  64 * time.Second,
		15: 64 * time.Second,
	}
	for retry, want := range wantBackoffs {
		if got := outboxBackoff(retry); got != want {
			t.Fatalf("outboxBackoff(%d) = %v, want %v", retry, got, want)
		}
	}

	store := &fakeOutboxStore{
		batches: [][]CommentOutbox{
			{
				{ID: 10, EventID: "e-ok", EventKey: "aid-ok", RetryCount: 2},
				{ID: 11, EventID: "e-retry", EventKey: "aid-fail", RetryCount: 3},
				{ID: 12, EventID: "e-dlq", EventKey: "aid-fail", RetryCount: outboxMaxRetries - 1},
			},
		},
	}

	pub := &fakeEventPublisher{
		sendFn: func(_ context.Context, msg *pkg.Message) error {
			if msg.Key() == "aid-fail" {
				return errors.New("broker unavailable")
			}
			return nil
		},
	}

	d := &Data{
		event:       pub,
		outboxStore: store,
		eventTopic:  "comment-event",
		log:         log.NewHelper(log.NewStdLogger(io.Discard)),
	}

	before := time.Now()
	if err := d.publishOutbox(context.Background()); err != nil {
		t.Fatalf("publishOutbox failed: %v", err)
	}

	if len(store.applied) != 1 || len(store.applied[0]) != 3 {
		t.Fatalf("expected 3 outcomes, got %+v", store.applied)
	}

	outcomes := store.applied[0]

	// ID 10: succeeded
	if !outcomes[0].Published || outcomes[0].Status != outboxStatusPublished {
		t.Fatalf("expected ID 10 published, got %+v", outcomes[0])
	}

	// ID 11: retry 3 -> 4 (backoff = 2^4 = 16s), stays pending
	if outcomes[1].Published || outcomes[1].Status != outboxStatusPending || outcomes[1].RetryCount != 4 {
		t.Fatalf("expected ID 11 pending with retry=4, got %+v", outcomes[1])
	}
	backoffDelta := outcomes[1].NextRetryAt.Sub(before)
	if backoffDelta < 15*time.Second || backoffDelta > 18*time.Second {
		t.Fatalf("expected ID 11 NextRetryAt ~16s from now, got %v", backoffDelta)
	}

	// ID 12: retry 9 -> 10 (>= outboxMaxRetries) -> dead_letter
	if outcomes[2].Published || outcomes[2].Status != outboxStatusDeadLetter || outcomes[2].RetryCount != outboxMaxRetries {
		t.Fatalf("expected ID 12 dead_letter with retry=%d, got %+v", outboxMaxRetries, outcomes[2])
	}
}

func TestOutboxDrainLoopContinuesOnFullBatchesAndCapsAtMaxIterations(t *testing.T) {
	makeBatch := func(n int, startID uint64) []CommentOutbox {
		batch := make([]CommentOutbox, n)
		for i := 0; i < n; i++ {
			id := startID + uint64(i)
			batch[i] = CommentOutbox{ID: id, EventID: fmt.Sprintf("e-%d", id), EventKey: "aid-1", Payload: []byte(`{}`)}
		}
		return batch
	}

	t.Run("drains until partial batch", func(t *testing.T) {
		store := &fakeOutboxStore{
			batches: [][]CommentOutbox{
				makeBatch(outboxBatchSize, 1),
				makeBatch(outboxBatchSize, 51),
				makeBatch(7, 101),
			},
		}
		pub := &fakeEventPublisher{}
		d := &Data{
			event:       pub,
			outboxStore: store,
			eventTopic:  "comment-event",
			log:         log.NewHelper(log.NewStdLogger(io.Discard)),
		}

		if err := d.publishOutbox(context.Background()); err != nil {
			t.Fatalf("publishOutbox failed: %v", err)
		}
		if store.claimCalls != 3 {
			t.Fatalf("expected 3 claim calls to drain 50+50+7 events, got %d", store.claimCalls)
		}
		if len(pub.sent) != 107 {
			t.Fatalf("expected 107 published events, got %d", len(pub.sent))
		}
	})

	t.Run("caps drain iterations per tick", func(t *testing.T) {
		batches := make([][]CommentOutbox, outboxMaxBatchesPerTick+5)
		for i := range batches {
			batches[i] = makeBatch(outboxBatchSize, uint64(i*outboxBatchSize+1))
		}
		store := &fakeOutboxStore{batches: batches}
		pub := &fakeEventPublisher{}
		d := &Data{
			event:       pub,
			outboxStore: store,
			eventTopic:  "comment-event",
			log:         log.NewHelper(log.NewStdLogger(io.Discard)),
		}

		if err := d.publishOutbox(context.Background()); err != nil {
			t.Fatalf("publishOutbox failed: %v", err)
		}
		if store.claimCalls != outboxMaxBatchesPerTick {
			t.Fatalf("expected cap at %d batches per tick, got %d", outboxMaxBatchesPerTick, store.claimCalls)
		}
	})
}

func TestGracefulShutdownOrderStopsOutboxBeforeClosingResources(t *testing.T) {
	var events []string
	var mu sync.Mutex
	record := func(ev string) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	inFlightStarted := make(chan struct{}, 1)
	releaseSend := make(chan struct{})

	store := &fakeOutboxStore{
		batches: [][]CommentOutbox{
			{{ID: 1, EventID: "e-shutdown", EventKey: "aid-1", Payload: []byte(`{}`)}},
		},
		eventsLog: &events,
		eventsMu:  &mu,
	}
	pub := &fakeEventPublisher{
		eventsLog: &events,
		eventsMu:  &mu,
		sendFn: func(ctx context.Context, _ *pkg.Message) error {
			select {
			case inFlightStarted <- struct{}{}:
			default:
			}
			<-releaseSend
			return nil
		},
	}

	d := &Data{
		event:          pub,
		outboxStore:    store,
		eventTopic:     "comment-event",
		log:            log.NewHelper(log.NewStdLogger(io.Discard)),
		outboxInterval: 10 * time.Millisecond,
	}

	d.startOutbox()

	// Wait until the background outbox worker is mid-flight inside Send().
	select {
	case <-inFlightStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for outbox worker to start sending")
	}

	shutdownDone := make(chan struct{})
	go func() {
		d.shutdown(
			func() error {
				record("grpc_close")
				return nil
			},
			func() error {
				record("db_close")
				return nil
			},
		)
		close(shutdownDone)
	}()

	// Shutdown MUST still be waiting for the in-flight outbox worker.
	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before in-flight outbox worker finished")
	case <-time.After(40 * time.Millisecond):
	}

	// Release in-flight Kafka Send so outbox applies outcome and exits cleanly.
	close(releaseSend)

	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shutdown to finish")
	}

	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()

	want := []string{"claim_tx", "kafka_send", "apply_tx", "kafka_close", "grpc_close", "db_close"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected shutdown sequence:\n got: %v\nwant: %v", got, want)
	}
}
