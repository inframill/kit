package harness

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/sir-hassan/inframill/pkg/harness/internal/dispatch"
	privatehttp "github.com/sir-hassan/inframill/pkg/harness/internal/httpserver"
)

const (
	componentHTTP    = "http"
	componentProcess = "process"
	fieldComponent   = "component"
	fieldEvent       = "event"
	fieldWorker      = "worker"
)

type configuredWorker struct {
	componentType string
	worker        Worker
}

type workerResult struct {
	componentType string
	err           error
}

// App is one immutable configured application and its owned lifecycle.
type App struct {
	main       MainConfig
	dispatcher *dispatch.Dispatcher
	handler    http.Handler
	workers    []configuredWorker
	logger     Logger
	cleanup    func(context.Context) error

	mu            sync.Mutex
	started       bool
	closed        bool
	shuttingDown  bool
	server        *http.Server
	serverResults chan error
	workerCancels []context.CancelFunc
	workerWait    sync.WaitGroup
	workerResults chan workerResult
	shutdownOnce  sync.Once
	shutdownDone  chan struct{}
	shutdownErr   error
}

func newApp(
	main MainConfig,
	dispatcher *dispatch.Dispatcher,
	workers []configuredWorker,
	logger Logger,
	cleanup func(context.Context) error,
) *App {
	app := &App{
		main: main, dispatcher: dispatcher, workers: append([]configuredWorker(nil), workers...),
		logger: logger, cleanup: cleanup, shutdownDone: make(chan struct{}),
	}
	app.handler = privatehttp.New(dispatcher, app.logHTTP)

	return app
}

// Run binds the listener, starts workers, and blocks until cancellation or failure.
func (app *App) Run(ctx context.Context) error {
	app.mu.Lock()
	if app.closed {
		app.mu.Unlock()
		return errors.New("inframill app is closed")
	}
	if app.started {
		app.mu.Unlock()
		return errors.New("inframill app has already run")
	}
	app.started = true
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", fmt.Sprintf(":%d", app.main.Port))
	if err != nil {
		app.mu.Unlock()
		shutdownCtx, cancel := automaticShutdownContext(ctx, app.main.ShutdownTimeout)
		defer cancel()
		shutdownErr := app.shutdown(shutdownCtx)

		return errors.Join(fmt.Errorf("listen on port %d: %w", app.main.Port, err), shutdownErr)
	}
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", app.main.Port),
		Handler:           app.handler,
		ReadHeaderTimeout: app.main.ReadHeaderTimeout,
		ReadTimeout:       app.main.ReadTimeout,
		WriteTimeout:      app.main.WriteTimeout,
		IdleTimeout:       app.main.IdleTimeout,
	}
	app.server = server
	app.workerResults = make(chan workerResult, len(app.workers))
	app.workerCancels = make([]context.CancelFunc, 0, len(app.workers))
	workerContexts := make([]context.Context, 0, len(app.workers))
	for range app.workers {
		workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		workerContexts = append(workerContexts, workerCtx)
		app.workerCancels = append(app.workerCancels, cancel)
	}

	serverResult := make(chan error, 1)
	app.serverResults = serverResult
	app.logger.Info(ctx, "application started",
		Field{Key: fieldEvent, Value: "process.started"}, Field{Key: fieldComponent, Value: componentProcess})
	go func() {
		serveErr := server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		if serveErr == nil {
			app.logger.Info(ctx, "HTTP server stopped",
				Field{Key: fieldEvent, Value: "server.stopped"}, Field{Key: fieldComponent, Value: componentHTTP})
		} else {
			app.logger.Error(ctx, "HTTP server failed",
				Field{Key: fieldEvent, Value: "server.failed"}, Field{Key: fieldComponent, Value: componentHTTP})
		}
		serverResult <- serveErr
	}()
	for index, worker := range app.workers {
		app.workerWait.Add(1)
		//nolint:contextcheck // Each worker intentionally uses its independently cancellable lifecycle context.
		go func(worker configuredWorker, workerCtx context.Context) {
			defer app.workerWait.Done()
			app.logger.Info(workerCtx, "worker started",
				Field{Key: fieldEvent, Value: "worker.started"}, Field{Key: fieldComponent, Value: "worker:" + worker.componentType},
				Field{Key: fieldWorker, Value: worker.componentType})
			err := worker.worker.Run(workerCtx)
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				app.logger.Error(workerCtx, "worker failed",
					Field{Key: fieldEvent, Value: "worker.failed"}, Field{Key: fieldComponent, Value: "worker:" + worker.componentType},
					Field{Key: fieldWorker, Value: worker.componentType})
			} else {
				app.logger.Info(workerCtx, "worker stopped",
					Field{Key: fieldEvent, Value: "worker.stopped"}, Field{Key: fieldComponent, Value: "worker:" + worker.componentType},
					Field{Key: fieldWorker, Value: worker.componentType})
			}
			app.workerResults <- workerResult{componentType: worker.componentType, err: err}
		}(worker, workerContexts[index])
	}
	app.logger.Info(ctx, "server listening",
		Field{Key: fieldEvent, Value: "server.listening"}, Field{Key: fieldComponent, Value: componentHTTP},
		Field{Key: "address", Value: listener.Addr().String()})
	app.mu.Unlock()

	var triggerErr error
	select {
	case <-ctx.Done():
	case <-app.shutdownDone:
		return app.shutdownErr
	case serveErr := <-serverResult:
		if !app.isShuttingDown() {
			if serveErr == nil {
				triggerErr = errors.New("HTTP server stopped unexpectedly")
			} else {
				triggerErr = fmt.Errorf("HTTP server failed: %w", serveErr)
			}
		}
	case result := <-app.workerResults:
		if !app.isShuttingDown() {
			if result.err == nil {
				triggerErr = fmt.Errorf("worker type %q stopped unexpectedly", result.componentType)
			} else {
				triggerErr = fmt.Errorf("worker type %q failed: %w", result.componentType, result.err)
			}
		}
	}

	shutdownCtx, cancel := automaticShutdownContext(ctx, app.main.ShutdownTimeout)
	defer cancel()
	shutdownErr := app.shutdown(shutdownCtx)

	return errors.Join(triggerErr, shutdownErr)
}

// Close gracefully releases every resource owned by the app. It is idempotent.
func (app *App) Close(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, app.main.ShutdownTimeout)
	defer cancel()

	return app.shutdown(shutdownCtx)
}

func (app *App) shutdown(ctx context.Context) error {
	app.shutdownOnce.Do(func() {
		defer close(app.shutdownDone)
		app.mu.Lock()
		app.shuttingDown = true
		server := app.server
		serverResults := app.serverResults
		cancels := append([]context.CancelFunc(nil), app.workerCancels...)
		app.mu.Unlock()
		app.logger.Info(ctx, "application shutdown started",
			Field{Key: fieldEvent, Value: "process.shutdown_started"}, Field{Key: fieldComponent, Value: componentProcess})

		shutdownErrs := shutdownHTTP(ctx, server, serverResults)
		for _, cancel := range slices.Backward(cancels) {
			cancel()
		}
		shutdownErrs = append(shutdownErrs, app.waitForWorkers(ctx)...)
		if app.cleanup != nil {
			if err := app.cleanup(ctx); err != nil {
				shutdownErrs = append(shutdownErrs, fmt.Errorf("application cleanup: %w", err))
			}
		}
		app.logger.Info(ctx, "application stopped",
			Field{Key: fieldEvent, Value: "process.stopped"}, Field{Key: fieldComponent, Value: componentProcess})
		if err := app.logger.Close(ctx); err != nil {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("close logger: %w", err))
		}
		app.mu.Lock()
		app.closed = true
		app.mu.Unlock()
		app.shutdownErr = errors.Join(shutdownErrs...)
	})
	<-app.shutdownDone

	return app.shutdownErr
}

func shutdownHTTP(ctx context.Context, server *http.Server, results <-chan error) []error {
	var shutdownErrs []error
	if server != nil {
		if err := server.Shutdown(ctx); err != nil {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("shut down HTTP server: %w", err))
			_ = server.Close()
		}
	}
	select {
	case serveErr := <-results:
		if serveErr != nil {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("HTTP server failed during shutdown: %w", serveErr))
		}
	default:
	}

	return shutdownErrs
}

func (app *App) waitForWorkers(ctx context.Context) []error {
	waited := make(chan struct{})
	go func() {
		app.workerWait.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		return app.drainWorkerErrors()
	case <-ctx.Done():
		return []error{fmt.Errorf("wait for workers: %w", ctx.Err())}
	}
}

func (app *App) drainWorkerErrors() []error {
	var workerErrs []error
	for {
		select {
		case result := <-app.workerResults:
			if result.err != nil && !errors.Is(result.err, context.Canceled) && !errors.Is(result.err, context.DeadlineExceeded) {
				workerErrs = append(workerErrs, fmt.Errorf("worker type %q failed during shutdown: %w", result.componentType, result.err))
			}
		default:
			return workerErrs
		}
	}
}

func (app *App) isShuttingDown() bool {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.shuttingDown
}

func (app *App) logHTTP(ctx context.Context, level privatehttp.Level, message string, fields ...any) {
	structured := make([]Field, 0, len(fields)/2)
	for index := 0; index+1 < len(fields); index += 2 {
		key, ok := fields[index].(string)
		if ok {
			structured = append(structured, Field{Key: key, Value: fields[index+1]})
		}
	}
	switch level {
	case privatehttp.LevelDebug:
		app.logger.Debug(ctx, message, structured...)
	case privatehttp.LevelInfo:
		app.logger.Info(ctx, message, structured...)
	case privatehttp.LevelError:
		app.logger.Error(ctx, message, structured...)
	default:
		app.logger.Info(ctx, message, structured...)
	}
}

func automaticShutdownContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}
