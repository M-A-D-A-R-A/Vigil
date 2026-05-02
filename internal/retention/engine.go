package retention

import (
	"context"
	"sync"
	"time"

	"vigil/internal/config"
	"vigil/internal/index"
	"vigil/internal/store/raw"
)

type Status struct {
	Enabled            bool   `json:"enabled"`
	DryRun             bool   `json:"dry_run"`
	Days               int    `json:"days"`
	SweepInterval      string `json:"sweep_interval"`
	LastRunAt          string `json:"last_run_at,omitempty"`
	LastSuccessAt      string `json:"last_success_at,omitempty"`
	LastError          string `json:"last_error,omitempty"`
	LastCutoffDay      string `json:"last_cutoff_day,omitempty"`
	LastDeletedDayDirs int    `json:"last_deleted_day_dirs"`
	LastDeletedFiles   int    `json:"last_deleted_files"`
	LastDeletedBytes   int64  `json:"last_deleted_bytes"`
}

type Engine struct {
	cfg    config.RetentionConfig
	raw    *raw.Store
	worker *index.Worker
	gate   *sync.RWMutex

	statusMu sync.RWMutex
	status   Status
	stop     chan struct{}
	wg       sync.WaitGroup
}

func New(cfg config.RetentionConfig, rawStore *raw.Store, worker *index.Worker, gate *sync.RWMutex) *Engine {
	if gate == nil {
		gate = &sync.RWMutex{}
	}

	return &Engine{
		cfg:    cfg,
		raw:    rawStore,
		worker: worker,
		gate:   gate,
		status: Status{
			Enabled:       cfg.Enabled,
			DryRun:        cfg.DryRun,
			Days:          cfg.Days,
			SweepInterval: cfg.SweepInterval.String(),
		},
		stop: make(chan struct{}),
	}
}

func (e *Engine) Start() {
	if !e.cfg.Enabled {
		return
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()

		_ = e.RunOnce(context.Background())

		ticker := time.NewTicker(e.cfg.SweepInterval)
		defer ticker.Stop()

		for {
			select {
			case <-e.stop:
				return
			case <-ticker.C:
				_ = e.RunOnce(context.Background())
			}
		}
	}()
}

func (e *Engine) Close() {
	close(e.stop)
	e.wg.Wait()
}

func (e *Engine) RunOnce(ctx context.Context) error {
	return e.runOnceAt(ctx, time.Now().UTC())
}

func (e *Engine) RunOnceAt(ctx context.Context, now time.Time) error {
	return e.runOnceAt(ctx, now.UTC())
}

func (e *Engine) Status() Status {
	e.statusMu.RLock()
	defer e.statusMu.RUnlock()
	return e.status
}

func (e *Engine) runOnceAt(ctx context.Context, now time.Time) error {
	e.updateStatus(func(status *Status) {
		status.LastRunAt = now.Format(time.RFC3339Nano)
		status.LastError = ""
	})

	if !e.cfg.Enabled {
		return nil
	}

	cutoffDay := retentionCutoffDay(now, e.cfg.Days)

	e.gate.Lock()
	defer e.gate.Unlock()

	summary, err := e.raw.PruneBeforeDay(cutoffDay, e.cfg.DryRun)
	if err != nil {
		e.updateStatus(func(status *Status) {
			status.LastCutoffDay = cutoffDay
			status.LastError = err.Error()
		})
		return err
	}

	e.updateStatus(func(status *Status) {
		status.LastCutoffDay = summary.CutoffDay
		status.LastDeletedDayDirs = summary.DeletedDayDirs
		status.LastDeletedFiles = summary.DeletedFiles
		status.LastDeletedBytes = summary.DeletedBytes
	})

	if e.cfg.DryRun || summary.DeletedDayDirs == 0 {
		e.updateStatus(func(status *Status) {
			status.LastSuccessAt = now.Format(time.RFC3339Nano)
		})
		return nil
	}

	if err := e.worker.RebuildNow(ctx, true); err != nil {
		e.updateStatus(func(status *Status) {
			status.LastError = err.Error()
		})
		return err
	}

	e.updateStatus(func(status *Status) {
		status.LastSuccessAt = now.Format(time.RFC3339Nano)
	})
	return nil
}

func (e *Engine) updateStatus(fn func(*Status)) {
	e.statusMu.Lock()
	defer e.statusMu.Unlock()
	fn(&e.status)
}

func retentionCutoffDay(now time.Time, days int) string {
	if days < 1 {
		days = 1
	}
	return now.UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
}
