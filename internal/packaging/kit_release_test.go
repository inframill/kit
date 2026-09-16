package packaging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKitAndBundleVersionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, selected, kit, core, bundle, bundledKit, bundledCore, want string
	}{
		{"aligned", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", ""},
		{"independent patch", "0.5.7", "0.5.7", "0.5.0-dev", "0.5.7", "0.5.7", "0.5.0-dev", ""},
		{"prerelease", "0.5.1-rc.1", "0.5.1-rc.1", "0.5.0-dev", "0.5.1-rc.1", "0.5.1-rc.1", "0.5.0-dev", ""},
		{"wrong minor", "0.6.0", "0.6.0", "0.5.0-dev", "0.6.0", "0.6.0", "0.5.0-dev", "major/minor"},
		{"wrong major", "1.5.0", "1.5.0", "0.5.0-dev", "1.5.0", "1.5.0", "0.5.0-dev", "major/minor"},
		{"wrong descriptor identity", "0.5.0-dev", "0.5.1", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "compiled target"},
		{"unsupported core", "0.5.0-dev", "0.5.0-dev", "0.5.1", "0.5.0-dev", "0.5.0-dev", "0.5.1", "compiled target"},
		{"wrong image identity", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "0.5.1", "0.5.0-dev", "0.5.0-dev", "match exactly"},
		{"wrong Kit in image", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "0.5.1", "0.5.0-dev", "match exactly"},
		{"wrong core in image", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "0.5.0-dev", "0.5.1", "differs from Kit target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write := func(path string, value any) {
				t.Helper()
				path = filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			// Driver versions deliberately differ from Kit and remain independent.
			write("releases/kits/"+tc.selected+".yaml", map[string]any{
				"version": tc.kit, "inframill_version": tc.core,
				"drivers": []map[string]string{{"name": "opentofu", "version": "0.1.0"}, {"name": "ansible", "version": "0.1.0"}},
			})
			write("releases/bundles/"+tc.selected+".lock.yaml", map[string]any{
				"version": tc.bundle, "kit_version": tc.bundledKit,
				"inframill": map[string]string{"version": tc.bundledCore},
			})
			_, err := Bundle(root, tc.selected)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}
