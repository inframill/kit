package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseActionsRejectMismatchedLineBeforeEffects(t *testing.T) {
	script, err := os.ReadFile("../../scripts/read-version.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"build", "bundle:validate", "bundle:push", "inframill:copy", "blueprint:requirements", "blueprint:package", "blueprints:package", "blueprint:push", "blueprints:push"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			for path, data := range map[string]string{
				"scripts/read-version.sh":          string(script),
				"VERSION":                          "0.7.1\n",
				"releases/kits/0.7.1.yaml":         `{"version":"0.7.1","inframill_version":"0.6.2"}`,
				"releases/bundles/0.7.1.lock.yaml": `{"version":"0.7.1","kit_version":"0.7.1","inframill":{"version":"0.6.2"}}`,
			} {
				path = filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(root)
			t.Setenv("NAMESPACE", "inframill")
			t.Setenv("BLUEPRINT_VERSION", "0.2.0")
			args := os.Args
			t.Cleanup(func() { os.Args = args })
			os.Args = []string{"kit-build", action}
			if err := run(); err == nil || !strings.Contains(err.Error(), "major/minor") {
				t.Fatalf("got %v, want major/minor rejection", err)
			}
			for _, dir := range []string{"bin", "dist"} {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("unexpected output %s: %v", dir, err)
				}
			}
		})
	}
}
