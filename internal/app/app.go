package app

import (
	"context"
	"net/http"
	"sync"

	"vigil/internal/config"
	"vigil/internal/httpapi"
	"vigil/internal/index"
	"vigil/internal/ingest"
	"vigil/internal/project"
	"vigil/internal/retention"
	"vigil/internal/store/raw"
	"vigil/internal/store/sqlite"
)

type App struct {
	Handler http.Handler

	db        *sqlite.Store
	worker    *index.Worker
	retention *retention.Engine
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	db, err := sqlite.Open(ctx, cfg.DBPath())
	if err != nil {
		return nil, err
	}

	rawStore := raw.NewStore(cfg.RawLogDir(), cfg.SegmentMaxBytes)
	projectService := project.NewService(db)
	worker := index.NewWorker(db, rawStore)
	retentionGate := &sync.RWMutex{}
	retentionEngine := retention.New(cfg.Retention, rawStore, worker, retentionGate)
	ingestService := ingest.NewService(projectService, rawStore, db, worker, cfg.MaxEventBytes, retentionGate)
	server, err := httpapi.New(cfg, projectService, ingestService, db, retentionEngine)
	if err != nil {
		db.Close()
		return nil, err
	}

	worker.Start()
	retentionEngine.Start()

	return &App{
		Handler:   server.Handler(),
		db:        db,
		worker:    worker,
		retention: retentionEngine,
	}, nil
}

func (a *App) Close() error {
	a.retention.Close()
	a.worker.Close()
	return a.db.Close()
}

func (a *App) RunRetentionNow(ctx context.Context) error {
	return a.retention.RunOnce(ctx)
}

func (a *App) RetentionStatus() retention.Status {
	return a.retention.Status()
}
