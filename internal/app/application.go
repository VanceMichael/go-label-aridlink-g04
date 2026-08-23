package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/alert"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/audit"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/delivery"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/grant"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/httpapi"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/idempotency"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/intervention"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/monitoring"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/outbox"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/program"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/query"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/review"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/site"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/technology"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/training"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/worker"
)

type Application struct {
	Config Config
	Store  *store.Store
	Server *http.Server
	Worker *worker.Runner
	logger *slog.Logger
}

func Build(ctx context.Context, cfg Config, logger *slog.Logger) (*Application, error) {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	clock := platform.SystemClock{}
	ids := platform.RandomIDs{}
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			st.Close()
		}
	}()
	if err := st.Migrate(ctx, cfg.MigrationDir); err != nil {
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	writer := audit.NewWriter(ids, clock)
	events := outbox.NewService(st, ids, clock)
	authService := auth.NewService(st, ids, clock, writer, cfg.SessionTTL)
	if err := authService.Bootstrap(ctx, cfg.BootstrapEmail, cfg.BootstrapPassword); err != nil {
		return nil, fmt.Errorf("bootstrap application: %w", err)
	}
	programs := program.NewService(st, ids, clock, writer, events)
	sites := site.NewService(st, ids, clock, writer, events)
	monitoringService := monitoring.NewService(st, ids, clock, writer, events)
	interventions := intervention.NewService(st, ids, clock, writer, events)
	evidenceService := evidence.NewService(st, ids, clock, writer, events)
	reviews := review.NewService(st, ids, clock, writer, events)
	grants := grant.NewService(st, ids, clock, writer, events)
	alerts := alert.NewService(st, ids, clock, writer, events)
	technologies := technology.NewService(st, ids, clock, writer, events)
	trainingService := training.NewService(st, ids, clock, writer, events)
	jobs := worker.NewJobs(st, ids, clock)
	idempotencyService := idempotency.NewService(st, ids, clock, 24*time.Hour)
	queries := query.NewService(st, clock)

	var sender delivery.Sender = delivery.LogSender{}
	if endpoint := os.Getenv("ARIDLINK_WEBHOOK_URL"); endpoint != "" {
		webhook, err := delivery.NewWebhookSender(&http.Client{Timeout: 10 * time.Second}, endpoint, os.Getenv("ARIDLINK_WEBHOOK_TOKEN"))
		if err != nil {
			return nil, err
		}
		sender = webhook
	}
	runner, err := worker.NewRunner(worker.Config{Owner: ids.New("worker"), Interval: cfg.WorkerInterval,
		Lease: cfg.WorkerLease, BatchSize: cfg.WorkerBatchSize, MaxAttempts: 8, RetryBase: time.Second,
		HandlerTimeout: 10 * time.Second}, clock, logger, events, jobs, alerts, grants, sender)
	if err != nil {
		return nil, err
	}
	router := httpapi.NewRouter(httpapi.Dependencies{Logger: logger, Clock: clock, IDs: ids, Store: st,
		Auth: authService, Idempotency: idempotencyService, Programs: programs, Queries: queries, Sites: sites, Monitoring: monitoringService,
		Intervention: interventions, Evidence: evidenceService, Reviews: reviews, Grants: grants,
		Alerts: alerts, Technology: technologies, Training: trainingService})
	server := &http.Server{Addr: cfg.Address, Handler: router, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	closeOnError = false
	return &Application{Config: cfg, Store: st, Server: server, Worker: runner, logger: logger}, nil
}

func (a *Application) Run(ctx context.Context) error {
	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	errorsChannel := make(chan error, 2)
	go func() {
		a.logger.Info("http server starting", "address", a.Server.Addr)
		if err := a.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errorsChannel <- fmt.Errorf("serve HTTP: %w", err)
		}
	}()
	go func() {
		if err := a.Worker.Run(runContext); err != nil && err != context.Canceled {
			errorsChannel <- fmt.Errorf("run worker: %w", err)
		}
	}()
	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errorsChannel:
		runErr = err
	}
	cancelRun()
	shutdownContext, cancel := context.WithTimeout(context.Background(), a.Config.ShutdownTimeout)
	defer cancel()
	if err := a.Server.Shutdown(shutdownContext); err != nil {
		return errors.Join(runErr, fmt.Errorf("shutdown HTTP server: %w", err))
	}
	return runErr
}

func (a *Application) Close() {
	a.Store.Close()
}
