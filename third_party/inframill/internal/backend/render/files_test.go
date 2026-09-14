package render

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sir-hassan/inframill/internal/platform/filetree"
)

// A valid template path with an oversized component fails during filesystem writes,
// after the instance directory and earlier source files have already been created.
func TestWriteFailureCleansOnlyNewInstance(t *testing.T) {
	source := map[string]filetree.File{
		"manifest.yaml":                        {Content: []byte("drivers: {}\n")},
		"schema.json":                          {Content: []byte("{}")},
		"template":                             {Directory: true},
		"template/" + strings.Repeat("x", 300): {Content: []byte("content")},
	}
	destination := filepath.Join(t.TempDir(), "instance")
	if err := Render(t.Context(), source, nil, destination); err == nil {
		t.Fatal("oversized filename accepted")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial render survived", err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(destination, "existing")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Render(t.Context(), source, nil, destination); !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "preserve" {
		t.Fatal("existing data changed", err)
	}
}

func TestTemplateRenderingAndCollisionValidation(t *testing.T) {
	files := map[string]filetree.File{
		"template":            {Directory: true},
		"template/empty":      {Directory: true},
		"template/run.gotmpl": {Content: []byte("{{ toJSON .message }}"), Executable: true},
		"template/plain":      {Content: []byte("{{ untouched }}")},
	}
	rendered, err := Build(files, map[string]any{"message": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered["run"].Content) != `"hello"` || !rendered["run"].Executable || !rendered["empty"].Directory || string(rendered["plain"].Content) != "{{ untouched }}" {
		t.Fatal(rendered)
	}
	for name, source := range map[string]map[string]filetree.File{
		"missing value":      files,
		"same destination":   {"template/a": {}, "template/a.gotmpl": {}},
		"file parent":        {"template/a": {}, "template/a/child": {}},
		"escape":             {"template/../outside": {}},
		"reserved artifacts": {"template/artifacts/file": {}},
		"bad template":       {"template/a.gotmpl": {Content: []byte("{{ broken")}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, buildErr := Build(source, nil); buildErr == nil {
				t.Fatal("invalid render accepted")
			}
		})
	}
}
