package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

func awaitRunningMetadata(t *testing.T, i *instance) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		info, err := readInfo(i.root, inframill.InstanceID(filepath.Base(i.root)))
		if err == nil && info.Status == inframill.InstanceRunning {
			return
		}
		select {
		case <-deadline:
			t.Fatal("delete did not reach namespace lock")
		case <-time.After(time.Millisecond):
		}
	}
}

// SPEC-0009 REQ-005: check both command admission and the final deletion
// boundary. Neither normal nor fallback metadata writes may touch a replacement.
func TestREQ005SubstitutionPreservesReplacement(t *testing.T) {
	for _, phase := range []string{"before-command", "before-removal"} {
		for _, kind := range []string{"directory", "symlink", "metadata", "parent", "parent-symlink"} {
			t.Run(phase+"/"+kind, func(t *testing.T) {
				b, i := fixture(t, 2, nil)
				must := func(err error) {
					t.Helper()
					if err != nil {
						t.Fatal(err)
					}
				}
				var unlock func()
				if phase == "before-removal" {
					lock, err := b.lockNamespace(t.Context())
					must(err)
					defer lock.Close()
					unlock = func() { must(lock.Close()) }
					_, err = i.SubmitCommand(t.Context(), inframill.CommandRequest{ID: inframill.CommandID(testID(10)), Command: deleteCommand})
					must(err)
					awaitRunningMetadata(t, i)
				}
				metadata, err := os.ReadFile(filepath.Join(i.root, "instance.json"))
				must(err)
				backup := filepath.Join(t.TempDir(), "original")
				switch kind {
				case "directory", "symlink":
					must(os.Rename(i.root, backup))
					replacement := i.root
					if kind == "symlink" {
						replacement = filepath.Join(t.TempDir(), "target")
					}
					must(os.Mkdir(replacement, 0700))
					must(os.WriteFile(filepath.Join(replacement, "instance.json"), metadata, 0600))
					must(os.WriteFile(filepath.Join(replacement, "sentinel"), []byte("replacement"), 0600))
					if kind == "symlink" {
						must(os.Symlink(replacement, i.root))
					}
				case "metadata":
					info, err := readInfo(i.root, inframill.InstanceID(filepath.Base(i.root)))
					must(err)
					info.ID = testID(99)
					must(writeInfo(i.root, info))
				case "parent", "parent-symlink":
					must(os.Rename(b.root, backup))
					if kind == "parent-symlink" {
						must(os.Symlink(backup, b.root))
					} else {
						must(os.MkdirAll(i.root, 0700))
						must(os.WriteFile(filepath.Join(i.root, "instance.json"), metadata, 0600))
					}
				}
				// Snapshot the target itself (including outside a substituted symlink).
				target, err := filepath.EvalSymlinks(i.root)
				must(err)
				before := storageSnapshot(t, target)
				if unlock != nil {
					unlock()
				} else {
					_, err := i.SubmitCommand(t.Context(), inframill.CommandRequest{ID: inframill.CommandID(testID(10)), Command: deleteCommand})
					if err != nil && !errors.Is(err, inframill.ErrInstanceFailed) {
						t.Fatal(err)
					}
				}
				waitFinished(t, i)
				if i.Deleted() || i.Info().Status != inframill.InstanceFailed {
					t.Fatal("unsafe deletion accepted", i.Info())
				}
				after := storageSnapshot(t, target)
				// SQLite records on the original open database may finish admission.
				// Metadata and all replacement bytes must remain unchanged.
				if kind == "metadata" || kind == "parent-symlink" {
					for _, snapshot := range []map[string]string{before, after} {
						delete(snapshot, "commands.sqlite")
						delete(snapshot, "commands.sqlite-journal")
					}
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatal("suspect path changed")
				}
			})
		}
	}
}

// SPEC-0009 REQ-004/005: a second Backend cannot initialize while deletion owns
// or waits on the namespace; reuse after deletion gets an independent UUID leaf.
func TestREQ005DeletionInitializationLock(t *testing.T) {
	b, i := fixture(t, 2, nil)
	lock, err := b.lockNamespace(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := i.SubmitCommand(t.Context(), inframill.CommandRequest{ID: inframill.CommandID(testID(10)), Command: deleteCommand}); err != nil {
		t.Fatal(err)
	}
	awaitRunningMetadata(t, i)
	other, err := New(b.root, testDrivers(t, []fakeDriver{{}}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	err = other.InitInstance(ctx, testID(2), "project/dev", testBlueprint(t), inframill.InstanceValues{messageKey: helloMessage})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("initialization bypassed namespace lock", err)
	}
	if _, err := os.Stat(i.root); err != nil {
		t.Fatal("deletion bypassed namespace lock", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	waitFinished(t, i)
	if !i.Deleted() {
		t.Fatal(i.Info())
	}
	initNamed(t, other, 2, "project/dev")
	if _, err := other.SpawnInstance(t.Context(), testID(1)); !errors.Is(err, inframill.ErrNotFound) {
		t.Fatal(err)
	}
}

// SPEC-0009 REQ-005: partial removal is a failure, not confirmed absence.
func TestREQ005PartialDeletionFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires an unprivileged process")
	}
	_, i := fixture(t, 2, nil)
	protected := filepath.Join(i.root, "artifacts", "protected")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protected, "sentinel"), []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(protected, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(protected, 0700)
	if _, err := i.SubmitCommand(t.Context(), inframill.CommandRequest{ID: inframill.CommandID(testID(10)), Command: deleteCommand}); err != nil {
		t.Fatal(err)
	}
	waitFinished(t, i)
	if i.Deleted() || i.Info().Status != inframill.InstanceFailed {
		t.Fatal(i.Info())
	}
	if data, err := os.ReadFile(filepath.Join(protected, "sentinel")); err != nil || string(data) != "preserved" {
		t.Fatal(string(data), err)
	}
}
