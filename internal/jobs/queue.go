package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jo-hoe/gostwriter/internal/common"
)

// WorkItem contains a copy of the job data needed for processing and a cleanup func for the temp image file.
type WorkItem struct {
	Job     Job
	Cleanup func() error
}

// Processor defines how to process a WorkItem.
type Processor interface {
	Process(ctx context.Context, item WorkItem) error
}

// Queue is an in-memory bounded queue for WorkItems with a worker pool.
type Queue struct {
	log        *slog.Logger
	ch         chan WorkItem
	workers    int
	wg         sync.WaitGroup
	cancelOnce sync.Once
	cancel     context.CancelFunc
	started    bool
	mu         sync.Mutex
}

// NewQueue creates a new Queue with the given capacity and worker count.
func NewQueue(logger *slog.Logger, capacity int, workers int) *Queue {
	if capacity <= 0 {
		capacity = common.DefaultQueueCapacity
	}
	if workers <= 0 {
		workers = common.DefaultWorkerCount
	}
	return &Queue{
		log:     logger,
		ch:      make(chan WorkItem, capacity),
		workers: workers,
	}
}

// Start launches worker goroutines that consume WorkItems and process them using the provided Processor.
func (q *Queue) Start(ctx context.Context, p Processor) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.started {
		return errors.New("queue already started")
	}
	ctx, cancel := context.WithCancel(ctx)
	q.cancel = cancel
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker(ctx, p, i)
	}
	q.started = true
	return nil
}

func (q *Queue) worker(ctx context.Context, p Processor, idx int) {
	defer q.wg.Done()
	log := q.log.With("worker", idx)
	for {
		select {
		case <-ctx.Done():
			log.Debug("worker stopping due to context cancellation")
			return
		case item, ok := <-q.ch:
			if !ok {
				log.Debug("queue closed, worker exiting")
				return
			}
			jobLog := log.With("job_id", item.Job.ID)
			jobLog.Info("processing job", "stage", item.Job.Stage)
			start := time.Now()
			if err := p.Process(ctx, item); err != nil {
				jobLog.Error("job processing failed", "err", err, "duration", time.Since(start))
			} else {
				jobLog.Info("job processed", "duration", time.Since(start))
			}
			// Ensure cleanup is attempted regardless of outcome.
			if item.Cleanup != nil {
				if err := item.Cleanup(); err != nil {
					jobLog.Warn("cleanup failed", "err", err)
				}
			}
		}
	}
}

// Enqueue adds a WorkItem to the queue (non-blocking if capacity allows).
func (q *Queue) Enqueue(item WorkItem) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.started {
		return errors.New("queue not started")
	}
	select {
	case q.ch <- item:
		return nil
	default:
		return errors.New("queue is full")
	}
}

// Shutdown gracefully stops accepting work and waits for workers to finish current items up to the provided deadline.
func (q *Queue) Shutdown(deadline time.Duration) {
	q.cancelOnce.Do(func() {
		// stop workers
		if q.cancel != nil {
			q.cancel()
		}
		// close channel to unblock workers if they are waiting on receive
		close(q.ch)

		// wait with deadline
		done := make(chan struct{})
		go func() {
			defer close(done)
			q.wg.Wait()
		}()

		if deadline <= 0 {
			<-done
			return
		}

		timer := time.NewTimer(deadline)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
			q.log.Warn("queue shutdown deadline reached; workers may still be running")
		}
	})
}