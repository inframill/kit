package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

func TestGracefulCloseDrainsAcceptedCommands(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	_, i := fixture(t, 2, func(ctx context.Context, _ drivers.ExecutionContext, args inframill.Arguments) (any, error) {
		if args[labelArgument] == "blocked" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return true, nil
	})
	first, err := i.SubmitCommand(t.Context(), request(10, "blocked"))
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	second, err := i.SubmitCommand(t.Context(), request(11, "later"))
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- i.Close(context.Background()) }()
	<-i.done
	if _, submitErr := i.SubmitCommand(t.Context(), request(12, "rejected")); !errors.Is(submitErr, inframill.ErrClosed) {
		t.Fatal(submitErr)
	}
	select {
	case <-closed:
		t.Fatal("closed before draining")
	default:
	}
	close(release)
	if closeErr := <-closed; closeErr != nil {
		t.Fatal(closeErr)
	}
	store, err := openStore(t.Context(), i.root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close() //nolint:errcheck // Read-only fixture query after shutdown.
	for _, id := range []inframill.CommandID{first, second} {
		c, readErr := store.get(t.Context(), id)
		if readErr != nil || c.Status != inframill.Succeeded {
			t.Fatal(c, readErr)
		}
	}
	retry := request(13, "later")
	retry.IdempotencyKey = "after-close"
	if _, submitErr := i.SubmitCommand(t.Context(), retry); !errors.Is(submitErr, inframill.ErrClosed) {
		t.Fatal(submitErr)
	}
	if i.Info().Status != inframill.InstanceIdle {
		t.Fatal(i.Info())
	}
}

func TestMetadataWriteFailureStopsQueuedEffects(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	effects := 0
	_, i := fixture(t, 2, func(ctx context.Context, _ drivers.ExecutionContext, _ inframill.Arguments) (any, error) {
		effects++
		if effects > 1 {
			t.Error("queued command executed after persistence failure")
		}
		close(entered)
		select {
		case <-release:
			return true, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if _, err := i.SubmitCommand(t.Context(), request(10, "first")); err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := i.SubmitCommand(t.Context(), request(11, "second")); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(i.root, "instance.json")
	if err := os.Remove(metadata); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(metadata, 0o700); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitFinished(t, i)
	if i.Info().Status != inframill.InstanceFailed || i.Info().Failure == nil {
		t.Fatal(i.Info())
	}
}

func TestLegacyAndUnsupportedStoresArePreserved(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, string(testID(1)))
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "instance.json"), []byte("old data"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, localErr := New(root, testDrivers(t, []fakeDriver{fakeDriver{}}), Options{})
	if localErr != nil {
		t.Fatal(localErr)
	}
	if _, discoveryErr := b.GetAllIDs(t.Context()); discoveryErr == nil {
		t.Fatal("legacy workspace accepted")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal(err)
	}
	b, i := fixture(t, 1, nil)
	if closeErr := i.Close(context.Background()); closeErr != nil {
		t.Fatal(closeErr)
	}
	store, localErr := openStore(t.Context(), i.root, false)
	if localErr != nil {
		t.Fatal(localErr)
	}
	if _, versionErr := store.db.ExecContext(t.Context(), "PRAGMA user_version=99"); versionErr != nil {
		t.Fatal(versionErr)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	restored, localErr := b.SpawnInstance(t.Context(), i.Info().ID)
	if localErr != nil || restored.Info().Status != inframill.InstanceFailed {
		t.Fatal(restored, localErr)
	}
	if _, err := os.Stat(filepath.Join(i.root, "commands.sqlite")); err != nil {
		t.Fatal(err)
	}
}

func TestRawUnfinishedRecordsAreInterruptedOnSpawn(t *testing.T) {
	b, i := fixture(t, 2, nil)
	if err := i.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(t.Context(), i.root, false)
	if err != nil {
		t.Fatal(err)
	}
	for n, status := range []inframill.CommandStatus{inframill.Queued, inframill.Running} {
		c := inframill.CommandExecution{Request: request(n+10, "crashed"), Status: status, CreatedAt: time.Now().UTC()}
		if status == inframill.Running {
			c.StartedAt = &c.CreatedAt
		}
		if insertErr := store.insert(t.Context(), c); insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	if closeErr := store.db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	restored, err := b.SpawnInstance(t.Context(), i.Info().ID)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close(context.Background()) //nolint:errcheck // Fixture cleanup.
	for _, n := range []int{10, 11} {
		c, readErr := restored.GetCommandExecution(t.Context(), inframill.CommandID(testID(n)))
		if readErr != nil || c.Status != inframill.Interrupted || c.Error == nil {
			t.Fatal(c, readErr)
		}
	}
}

func TestCloseDeadlineDoesNotWaitForStorageMutex(t *testing.T) {
	_, i := fixture(t, 1, nil)
	i.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- i.Close(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			i.mu.Unlock()
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		i.mu.Unlock()
		t.Fatal("Close waited for storage mutex")
	}
	i.mu.Unlock()
	waitFinished(t, i)
}
