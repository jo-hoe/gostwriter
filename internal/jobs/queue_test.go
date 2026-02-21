package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"
)

type noopProcessor struct {
	count int32
	fail  bool
}

func (p *noopProcessor) Process(ctx context.Context, item WorkItem) error {
	atomic.AddInt32(&p.count, 1)
	if item.Cleanup != nil {
		_ = item.Cleanup()
	}
	if p.fail {
		return errors.New("fail")
	}
	return nil
}

func TestQueue_StartEnqueueShutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelError}))
	q := NewQueue(logger, 2, 1)
	p := &noopProcessor{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := q.Start(ctx, p); err != nil {
		t.Fatalf("queue start: %v", err)
	}

	item := WorkItem{Job: Job{ID: "id1"}}
	if err := q.Enqueue(item); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// allow worker to process
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&p.count) < 1 {
		t.Fatalf("expected processor to be called at least once")
	}

	// shutdown should complete promptly
	q.Shutdown(2 * time.Second)
}

func TestQueue_EnqueueBeforeStartFails(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelError}))
	q := NewQueue(logger, 1, 1)
	err := q.Enqueue(WorkItem{Job: Job{ID: "x"}})
	if err == nil {
		t.Fatalf("enqueue before start should error")
	}
}