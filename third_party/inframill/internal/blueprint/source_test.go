package blueprint_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sir-hassan/inframill/internal/blueprint"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

func TestRepositoryValuesAndMetadata(t *testing.T) {
	source, err := blueprint.New(filepath.Join("..", "..", "blueprints"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := source.List(t.Context())
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	for name, file := range map[string]string{"debug": "debug.json"} {
		b, err := source.Resolve(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		if b.Repository != name || !filepath.IsAbs(b.Directory) || !strings.HasPrefix(b.Digest, "sha256:") {
			t.Fatal(b)
		}
		raw, err := os.ReadFile(filepath.Join("..", "..", "examples", file))
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Values inframill.InstanceValues `json:"values"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		if err := source.ValidateValues(t.Context(), b, config.Values); err != nil {
			t.Fatal(name, err)
		}
	}
}

func TestSourceDoesNotInterpretDriverConfigsOrTemplates(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test-cloud")
	if err := os.MkdirAll(filepath.Join(dir, "template"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"manifest.yaml": "api_version: inframill.dev/blueprint/v1\ndescription: opaque\ndrivers: {custom: {whatever: true}}\n", "schema.json": `{"type":"object","required":["size"]}`, "template/file.gotmpl": "{{ broken"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source, checkErr := blueprint.New(root)
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	first, checkErr := source.Resolve(t.Context(), "test-cloud")
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	if err := source.ValidateValues(t.Context(), first, inframill.InstanceValues{}); err == nil {
		t.Fatal("missing value accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "template/file.gotmpl"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, checkErr := source.Resolve(t.Context(), "test-cloud")
	if checkErr != nil || first.Digest == second.Digest {
		t.Fatal(second, checkErr)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "template/link")); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Resolve(t.Context(), "test-cloud"); err == nil {
		t.Fatal("link accepted")
	}
}
