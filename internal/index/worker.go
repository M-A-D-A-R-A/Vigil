package index

import (
	"context"
	"sync"
	"time"

	"vigil/internal/event"
	"vigil/internal/store/raw"
	"vigil/internal/store/sqlite"
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
			return
		case ev := <-w.queue:
			if _, err := w.store.UpsertEvent(ev); err != nil {
				_ = w.store.MarkWorkerError(err)
				w.ScheduleRebuild()
			}
		case request := <-w.rebuild:
			err := w.performRebuild(request)
			if request.done != nil {
				request.done <- err
			}
		}
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
	err := w.raw.Replay(ctx, func(ev *event.StoredEvent) error {
		if ev.ReceivedAt > latestReceivedAt {
			latestReceivedAt = ev.ReceivedAt
		}
		_, err := w.store.UpsertEvent(ev)
		return err
	})
	if err != nil {
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
