package index

import (
	"context"
	"sync"
	"time"

	"vigil/internal/event"
	"vigil/internal/store/raw"
	"vigil/internal/store/sqlite"
)

const (
	batchSize    = 500
	batchMaxWait = 100 * time.Millisecond
)

type Worker struct {
	store   *sqlite.Store
	raw     *raw.Store
	queue   chan *event.StoredEvent
	rebuild chan rebuildRequest
	stop    chan struct{}
	wg      sync.WaitGroup
}

type rebuildRequest struct {
	ctx   context.Context
	reset bool
	done  chan error
}

type drainResult struct {
	stop    bool
	rebuild *rebuildRequest
}

func NewWorker(store *sqlite.Store, rawStore *raw.Store) *Worker {
	return &Worker{
		store:   store,
		raw:     rawStore,
		queue:   make(chan *event.StoredEvent, 256),
		rebuild: make(chan rebuildRequest, 1),
		stop:    make(chan struct{}),
	}
}

func (w *Worker) Start() {
	w.wg.Add(1)
	go w.run()
	w.ScheduleRebuild()
}

func (w *Worker) Close() {
	close(w.stop)
	w.wg.Wait()
}

func (w *Worker) Enqueue(ev *event.StoredEvent) bool {
	select {
	case w.queue <- ev:
		return true
	default:
		w.ScheduleRebuild()
		return false
	}
}

func (w *Worker) ScheduleRebuild() {
	select {
	case w.rebuild <- rebuildRequest{}:
	default:
	}
}

func (w *Worker) RebuildNow(ctx context.Context, reset bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	request := rebuildRequest{
		ctx:   ctx,
		reset: reset,
		done:  make(chan error, 1),
	}

	select {
	case w.rebuild <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-w.stop:
		return context.Canceled
	}

	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-w.stop:
		return context.Canceled
	}
}

func (w *Worker) run() {
	defer w.wg.Done()

	for {
		select {
		case <-w.stop:
			w.flushRemainingQueue()
			return
		case ev := <-w.queue:
			batch, result := w.drainBatch(ev)
			w.writeBatch(batch)
			if result.rebuild != nil {
				w.handleRebuild(*result.rebuild)
			}
			if result.stop {
				w.flushRemainingQueue()
				return
			}
		case request := <-w.rebuild:
			w.handleRebuild(request)
		}
	}
}

func (w *Worker) drainBatch(first *event.StoredEvent) ([]*event.StoredEvent, drainResult) {
	batch := []*event.StoredEvent{first}
	timer := time.NewTimer(batchMaxWait)
	defer timer.Stop()

	for len(batch) < batchSize {
		select {
		case ev := <-w.queue:
			batch = append(batch, ev)
		case request := <-w.rebuild:
			return batch, drainResult{rebuild: &request}
		case <-w.stop:
			return batch, drainResult{stop: true}
		case <-timer.C:
			return batch, drainResult{}
		}
	}

	return batch, drainResult{}
}

func (w *Worker) writeBatch(batch []*event.StoredEvent) {
	if len(batch) == 0 {
		return
	}
	if _, _, err := w.store.UpsertEvents(batch); err != nil {
		_ = w.store.MarkWorkerError(err)
		w.ScheduleRebuild()
	}
}

func (w *Worker) flushRemainingQueue() {
	batch := make([]*event.StoredEvent, 0, batchSize)
	for {
		select {
		case ev := <-w.queue:
			batch = append(batch, ev)
			if len(batch) >= batchSize {
				w.writeBatch(batch)
				batch = make([]*event.StoredEvent, 0, batchSize)
			}
		default:
			w.writeBatch(batch)
			return
		}
	}
}

func (w *Worker) handleRebuild(request rebuildRequest) {
	err := w.performRebuild(request)
	if request.done != nil {
		request.done <- err
	}
}

func (w *Worker) performRebuild(request rebuildRequest) error {
	ctx := request.ctx
	if ctx == nil {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ctx = timeoutCtx
	}

	if request.reset {
		if err := w.store.ResetReadModels(time.Now().UTC()); err != nil {
			_ = w.store.MarkWorkerError(err)
			return err
		}
	}

	latestReceivedAt := ""
	batch := make([]*event.StoredEvent, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if _, _, err := w.store.UpsertEvents(batch); err != nil {
			return err
		}
		batch = make([]*event.StoredEvent, 0, batchSize)
		return nil
	}

	err := w.raw.Replay(ctx, func(ev *event.StoredEvent) error {
		if ev.ReceivedAt > latestReceivedAt {
			latestReceivedAt = ev.ReceivedAt
		}
		batch = append(batch, ev)
		if len(batch) >= batchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		_ = w.store.MarkWorkerError(err)
		return err
	}
	if err := flush(); err != nil {
		_ = w.store.MarkWorkerError(err)
		return err
	}

	now := time.Now().UTC()
	if request.reset {
		if err := w.store.ReplaceWorkerStateAfterReset(latestReceivedAt, now); err != nil {
			_ = w.store.MarkWorkerError(err)
			return err
		}
		return nil
	}

	if err := w.store.MarkRebuildSuccess(now); err != nil {
		_ = w.store.MarkWorkerError(err)
		return err
	}
	return nil
}
