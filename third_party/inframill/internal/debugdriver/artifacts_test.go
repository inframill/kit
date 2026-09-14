package debugdriver

import (
	"os"
	"path/filepath"
	"testing"
)

// SPEC-0000 REQ-013: first-party directory artifacts retain private bounded access.
func TestDirectoryArtifacts(t *testing.T) {
	files := directoryArtifacts{directory: t.TempDir()}
	if err := files.WriteFile(t.Context(), "sample.json", []byte(`{"message":"first"}`)); err != nil {
		t.Fatal(err)
	}
	if err := files.WriteFile(t.Context(), "sample.json", []byte(`{"message":"updated"}`)); err != nil {
		t.Fatal(err)
	}
	data, err := files.ReadFile(t.Context(), "sample.json")
	if err != nil || string(data) != `{"message":"updated"}` {
		t.Fatal(string(data), err)
	}
	info, err := os.Stat(filepath.Join(files.directory, "sample.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
	for _, name := range []string{"", "..", "../outside", "a/b", `a\b`} {
		if err := files.WriteFile(t.Context(), name, nil); err == nil {
			t.Fatal("unsafe artifact name accepted", name)
		}
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(files.directory, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := files.ReadFile(t.Context(), "link"); err == nil {
		t.Fatal("symlink read accepted")
	}
	if err := files.WriteFile(t.Context(), "link", nil); err == nil {
		t.Fatal("symlink write accepted")
	}
	if err := files.WriteFile(t.Context(), "too-large", make([]byte, (64<<20)+1)); err == nil {
		t.Fatal("oversized artifact write accepted")
	}
}
