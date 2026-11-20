package pkg

import (
	"context"
	"errors"
	"slices"
	"strconv"
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
