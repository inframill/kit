package backend

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

const (
	namedFixtureName = "project/dev"
)

func namedBackend(t *testing.T) *Backend {
	t.Helper()
	b, err := New(filepath.Join(t.TempDir(), "instances"), testDrivers(t, []fakeDriver{fakeDriver{}}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func initNamed(t *testing.T, b *Backend, n int, name string) {
	t.Helper()
	if err := b.InitInstance(t.Context(), testID(n), name, testBlueprint(t), inframill.InstanceValues{messageKey: helloMessage}); err != nil {
		t.Fatal(err)
	}
}

// SPEC-0009 REQ-001/002/005/006: UUIDs address disk; names remain metadata.
func TestUUIDStorageLifecycle(t *testing.T) {
	b := namedBackend(t)
	names := []string{"zeta/dev", "alpha/dev", "alpha/other"}
	for n, name := range names {
		initNamed(t, b, n+1, name)
		root := filepath.Join(b.root, string(testID(n+1)))
		for _, path := range []string{root, filepath.Dir(root), filepath.Join(root, "artifacts"), filepath.Join(root, "logs"), filepath.Join(root, "workspace")} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm()&0o077 != 0 {
				t.Fatal("nonprivate UUID storage directory", path, err)
			}
		}
		info, err := readInfo(root, testID(n+1))
		if err != nil || info.Name != name {
			t.Fatal(info, err)
		}
		if _, err := os.Stat(filepath.Join(b.root, filepath.FromSlash(name))); !os.IsNotExist(err) {
			t.Fatal("name alias created", err)
		}
	}
	ids, err := b.GetAllIDs(t.Context())
	if err != nil || !reflect.DeepEqual(ids, []inframill.InstanceID{testID(1), testID(2), testID(3)}) {
		t.Fatal("IDs not sorted by UUID", ids, err)
	}
	// Reconstruct the Backend to prove lookup comes from disk, not an in-memory map.
	reopened, err := New(b.root, testDrivers(t, []fakeDriver{fakeDriver{}}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	current, err := reopened.SpawnInstance(t.Context(), testID(2))
	if err != nil || current.Info().Name != names[1] {
		t.Fatal(current, err)
	}
	t.Cleanup(func() { _ = current.Close(context.Background()) }) //nolint:errcheck // Fixture may already be deleted.
	command := request(50, "run")
	if _, err := current.SubmitCommand(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if got := waitCommand(t, current, command.ID); got.Status != inframill.Succeeded {
		t.Fatal(got)
	}
	deletion := request(51, "delete")
	deletion.Command = deleteCommand
	deletion.Args = inframill.Arguments{}
	if _, err := current.SubmitCommand(t.Context(), deletion); err != nil {
		t.Fatal(err)
	}
	owned, ok := current.(*instance)
	if !ok {
		t.Fatal("unexpected Instance implementation")
	}
	waitFinished(t, owned)
	if _, err := os.Stat(filepath.Join(b.root, string(testID(2)))); !os.IsNotExist(err) {
		t.Fatal("deleted leaf exists", err)
	}
	if _, err := readInfo(filepath.Join(b.root, string(testID(3))), testID(3)); err != nil {
		t.Fatal("sibling changed", err)
	}
	initNamed(t, b, 4, names[1])
	if _, err := reopened.SpawnInstance(t.Context(), testID(2)); !errors.Is(err, inframill.ErrNotFound) {
		t.Fatal("old UUID selected replacement", err)
	}
	idsAfter, discoverErr := b.GetAllIDs(t.Context())
	if discoverErr != nil || !reflect.DeepEqual(idsAfter, []inframill.InstanceID{testID(1), testID(3), testID(4)}) {
		t.Fatal(idsAfter, discoverErr)
	}
}

// SPEC-0009 REQ-003/004: separate Backend objects share the filesystem initialization lock.
func TestUUIDStorageConcurrentCollisions(t *testing.T) {
	for _, sameID := range []bool{false, true} {
		b := namedBackend(t)
		bp := testBlueprint(t)
		const count = 6
		outcomes := make(chan error, count)
		var group sync.WaitGroup
		for n := range count {
			other, err := New(b.root, testDrivers(t, []fakeDriver{fakeDriver{}}), Options{})
			if err != nil {
				t.Fatal(err)
			}
			id, name := testID(n+1), namedFixtureName
			if sameID {
				id, name = testID(1), "project/instance-"+string(rune('a'+n))
			}
			group.Go(func() {
				outcomes <- other.InitInstance(t.Context(), id, name, bp, inframill.InstanceValues{messageKey: helloMessage})
			})
		}
		group.Wait()
		close(outcomes)
		winners := 0
		for err := range outcomes {
			if err == nil {
				winners++
			} else if !errors.Is(err, inframill.ErrAlreadyExists) {
				t.Fatal(err)
			}
		}
		ids, err := b.GetAllIDs(t.Context())
		if err != nil || winners != 1 || len(ids) != 1 {
			t.Fatal("collision published multiple Instances", winners, ids, err)
		}
	}
}

// SPEC-0009 REQ-003/004: ambiguous identities and unsafe layouts are never opened.
func TestUUIDStorageDiscoveryRejections(t *testing.T) {
	for _, kind := range []string{"missing", "malformed", "trailing-json", "mismatch", "duplicate-name", "instance-link", "metadata-link", "invalid-leaf", "named", "empty-project", "mixed", "uuid-project", "unmarked", "unknown-layout", "unknown-version"} {
		t.Run(kind, func(t *testing.T) {
			b := namedBackend(t)
			initNamed(t, b, 1, namedFixtureName)
			root := filepath.Join(b.root, string(testID(1)))
			metadata := filepath.Join(root, "instance.json")
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "missing":
				must(os.Remove(metadata))
			case "malformed":
				must(os.WriteFile(metadata, []byte("invalid"), 0600))
			case "trailing-json":
				data, err := os.ReadFile(metadata)
				must(err)
				must(os.WriteFile(metadata, append(data, []byte(" {}")...), 0600))
			case "mismatch":
				must(os.Rename(root, filepath.Join(b.root, string(testID(2)))))
			case "duplicate-name":
				other := filepath.Join(b.root, string(testID(2)))
				must(os.Mkdir(other, 0700))
				info, err := readInfo(root, testID(1))
				must(err)
				info.ID = testID(2)
				must(writeInfo(other, info))
			case "instance-link":
				must(os.Symlink(root, filepath.Join(b.root, string(testID(2)))))
			case "metadata-link":
				must(os.Rename(metadata, filepath.Join(root, "original.json")))
				must(os.Symlink(filepath.Join(root, "original.json"), metadata))
			case "invalid-leaf":
				must(os.Rename(root, filepath.Join(b.root, "Invalid")))
			case "named":
				must(os.Mkdir(filepath.Join(b.root, "project"), 0700))
				must(os.Rename(root, filepath.Join(b.root, "project", "dev")))
			case "empty-project":
				must(os.Mkdir(filepath.Join(b.root, "empty"), 0700))
			case "mixed":
				must(os.MkdirAll(filepath.Join(b.root, "project", "dev"), 0700))
				must(os.WriteFile(filepath.Join(b.root, "project", "dev", "sentinel"), []byte("old"), 0600))
			case "uuid-project":
				project := filepath.Join(b.root, string(testID(2)))
				must(os.Mkdir(project, 0700))
				must(os.Rename(root, filepath.Join(project, "dev")))
			default:
				data, err := os.ReadFile(metadata)
				must(err)
				var doc map[string]any
				must(json.Unmarshal(data, &doc))
				switch kind {
				case "unmarked":
					delete(doc, "storage_layout")
				case "unknown-layout":
					doc["storage_layout"] = "uuid-v99"
				case "unknown-version":
					doc["api_version"] = "inframill.dev/instance/v99"
				}
				data, err = json.Marshal(doc)
				must(err)
				must(os.WriteFile(metadata, data, 0600))
			}
			before := storageSnapshot(t, b.root)
			if _, err := b.GetAllIDs(t.Context()); err == nil {
				t.Fatal("invalid discovery accepted")
			}
			if _, err := b.SpawnInstance(t.Context(), testID(1)); err == nil {
				t.Fatal("invalid Instance spawned")
			}
			if err := b.InitInstance(t.Context(), testID(3), "safe/new", testBlueprint(t), inframill.InstanceValues{}); err == nil {
				t.Fatal("initialized beside ambiguous data")
			}
			if after := storageSnapshot(t, b.root); !reflect.DeepEqual(before, after) {
				t.Fatal("rejection changed existing storage", before, after)
			}
			if len(b.active) != 0 {
				t.Fatal("rejection started Instances")
			}
		})
	}
}

// Include file bytes, directories, and link targets, but exclude the namespace lock.
func storageSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".initialization.lock" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot[relative] = "link:" + target
		} else if entry.IsDir() {
			snapshot[relative] = "dir"
		} else {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[relative] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// SPEC-0009 REQ-003/004: valid identities retain per-Instance execution failure isolation.
func TestUUIDStorageReadableFailureIsolation(t *testing.T) {
	b := namedBackend(t)
	initNamed(t, b, 1, "project/broken")
	initNamed(t, b, 2, "project/healthy")
	root := filepath.Join(b.root, string(testID(1)))
	info, err := readInfo(root, testID(1))
	if err != nil {
		t.Fatal(err)
	}
	info.Values = nil
	if err := writeInfo(root, info); err != nil {
		t.Fatal(err)
	}
	ids, discoveryErr := b.GetAllIDs(t.Context())
	if discoveryErr != nil || len(ids) != 2 {
		t.Fatal(ids, discoveryErr)
	}
	for n, want := range []inframill.InstanceStatus{inframill.InstanceFailed, inframill.InstanceIdle} {
		i, err := b.SpawnInstance(t.Context(), testID(n+1))
		if err != nil || i.Info().Status != want {
			t.Fatal(i, err)
		}
		if err := i.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}
