package blueprint

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// SPEC-0000 REQ-011: unsupported format versions reject without rewriting data.
func TestUnsupportedSourceAndIndexVersionsPreserveData(t *testing.T) {
	root := t.TempDir()
	cloud := filepath.Join(root, "cloud")
	if err := os.MkdirAll(filepath.Join(cloud, "template"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte("api_version: inframill.dev/blueprint/v2\ndescription: unsupported fixture\ndrivers: {debug: {}}\n")
	if err := os.WriteFile(filepath.Join(cloud, "manifest.yaml"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloud, "schema.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, resolveErr := source.Resolve(t.Context(), "cloud"); resolveErr == nil {
		t.Fatal("unsupported Blueprint version accepted")
	}
	stored, err := os.ReadFile(filepath.Join(cloud, "manifest.yaml"))
	if err != nil || !bytes.Equal(stored, manifest) {
		t.Fatal("manifest was modified", err)
	}
	indexRoot := t.TempDir()
	index := []byte(`{"version":"inframill.dev/packages/v2","packages":{},"tags":{}}`)
	if writeErr := os.WriteFile(filepath.Join(indexRoot, "index.json"), index, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	store, err := NewStore(indexRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, listErr := store.List(t.Context()); listErr == nil {
		t.Fatal("unsupported package index accepted")
	}
	stored, err = os.ReadFile(filepath.Join(indexRoot, "index.json"))
	if err != nil || !bytes.Equal(stored, index) {
		t.Fatal("index was modified", err)
	}
}
