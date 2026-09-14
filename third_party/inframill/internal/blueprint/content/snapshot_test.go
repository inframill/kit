package content

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// SPEC-0000 REQ-007: runtime data is excluded at the source root only.
func TestSnapshotExcludesWorkspace(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"template/.inframill", "template/runtime"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ordinary := filepath.Join(root, "template/.inframill/ordinary.txt")
	if err := os.WriteFile(ordinary, []byte("template content"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, snapshotErr := Snapshot(root)
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if string(before["template/.inframill/ordinary.txt"].Content) != "template content" || !before["template/runtime"].Directory {
		t.Fatal("nested template content omitted", before)
	}
	for _, excluded := range []string{".inframill", "runtime"} {
		dir := filepath.Join(root, excluded)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state"), []byte("runtime-only"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Traversing this link would fail normal source validation.
		if err := os.Symlink(filepath.Join(root, "missing-target"), filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
	}
	after, snapshotErr := Snapshot(root)
	if snapshotErr != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("runtime changed snapshot", snapshotErr, after)
	}
	if err := os.WriteFile(ordinary, []byte("changed source"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := Snapshot(root)
	if err != nil || reflect.DeepEqual(before, changed) {
		t.Fatal("ordinary source change was ignored", err)
	}
}
