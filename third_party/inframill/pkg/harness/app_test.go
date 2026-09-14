package harness_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sir-hassan/inframill/pkg/harness"
)

func TestAppRunsHandlersAndWorkersAndShutsDownInOrder(t *testing.T) {
	port := availablePort(t)
	logger := &testLogger{}
	started := make(chan struct{})
	var once sync.Once
	worker := harness.WorkerFunc(func(ctx context.Context) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		logger.mu.Lock()
		logger.events = append(logger.events, "worker")
		logger.mu.Unlock()
		return nil
	})
	app, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader: harness.ConfigLoaderFunc(func(context.Context) (harness.Config, error) {
			return harness.Config{
				Main:     harness.MainConfig{Port: port, ShutdownTimeout: 2 * time.Second},
				Logger:   harness.LoggerConfig{Type: "test"},
				Handlers: []harness.HandlerConfig{{Type: "ready"}},
				Workers:  []harness.WorkerConfig{{Type: "job"}},
			}, nil
		}),
		LoggerFactory: &loggerFactory{logger: logger},
		HandlerFactories: handlerFactories(&handlerFactory{handlers: map[string]http.Handler{
			"ready": http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			}),
		}}, "ready"),
		WorkerFactories: workerFactories(&workerFactory{workers: map[string]harness.Worker{"job": worker}}, "job"),
		Cleanup: func(context.Context) error {
			logger.mu.Lock()
			defer logger.mu.Unlock()
			logger.events = append(logger.events, "cleanup")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start")
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/ready", port)
	var response *http.Response
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		response, err = http.Get(url) //nolint:gosec,noctx // Local one-shot test probe.
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("request app: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("response status=%d cors=%q", response.StatusCode, response.Header.Get("Access-Control-Allow-Origin"))
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if err := app.Run(context.Background()); err == nil {
		t.Fatal("second Run error = nil")
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if !reflect.DeepEqual(logger.events, []string{"worker", "cleanup", "logger"}) {
		t.Fatalf("shutdown events = %v", logger.events)
	}
}

func TestAppWorkerFailureUsesConfiguredType(t *testing.T) {
	workerErr := fmt.Errorf("queue unavailable")
	app, err := harness.Build(context.Background(), harness.Options{
		ConfigLoader: harness.ConfigLoaderFunc(func(context.Context) (harness.Config, error) {
			return harness.Config{
				Main:    harness.MainConfig{Port: availablePort(t), ShutdownTimeout: 2 * time.Second},
				Logger:  harness.LoggerConfig{Type: "test"},
				Workers: []harness.WorkerConfig{{Type: "notifications"}},
			}, nil
		}),
		LoggerFactory:    &loggerFactory{logger: &testLogger{}},
		HandlerFactories: harness.HandlerFactories{},
		WorkerFactories: workerFactories(&workerFactory{workers: map[string]harness.Worker{
			"notifications": harness.WorkerFunc(func(context.Context) error { return workerErr }),
		}}, "notifications"),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = app.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `worker type "notifications" failed`) || !strings.Contains(err.Error(), workerErr.Error()) {
		t.Fatalf("Run error = %v", err)
	}
}

func availablePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	return port
}
