package backend

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// SPEC-0000 REQ-011: unsupported saved execution manifests fail without modifying user files.
func TestUnsupportedExecutionManifestIsPreserved(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "manifest.yaml")
	data := []byte("api_version: inframill.dev/blueprint/v2\ndrivers: {debug: {}}\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(root); err == nil {
		t.Fatal("unsupported execution manifest accepted")
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatal("saved manifest was modified", err)
	}
}

// SPEC-0006 REQ-004: the prerelease rename rejects legacy saved manifests without rewriting them.
func TestREQ004LegacySystemsManifestIsPreserved(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "manifest.yaml")
	data := []byte("api_version: inframill.dev/blueprint/v1\nsystems: {debug: {}}\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(root); err == nil {
		t.Fatal("legacy systems accepted")
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatal("legacy data modified", err)
	}
}
