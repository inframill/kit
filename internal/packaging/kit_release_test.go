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
		name, selected, kit, core, want string
	}{
		{"aligned", "0.6.0-dev", "0.6.0-dev", "0.6.0-dev", ""},
		{"independent patch", "0.6.7", "0.6.7", "0.6.0-dev", ""},
		{"prerelease", "0.6.1-rc.1", "0.6.1-rc.1", "0.6.0-dev", ""},
		{"future release line", "2.9.3", "2.9.3", "2.9.7-rc.1", ""},
		{"missing core", "0.6.0", "0.6.0", "", "exact Inframill SemVer"},
		{"invalid core", "0.6.0", "0.6.0", "0.6", "exact Inframill SemVer"},
		{"wrong minor", "0.7.0", "0.7.0", "0.6.0-dev", "major/minor"},
		{"wrong major", "1.6.0", "1.6.0", "0.6.0-dev", "major/minor"},
		{"wrong identity", "0.6.0-dev", "0.6.1", "0.6.0-dev", "identity differs"},
		{"independent engine patch", "0.6.0-dev", "0.6.0-dev", "0.6.1", ""},
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
			write("releases/"+tc.selected+".lock.json", map[string]any{
				"version": tc.kit, "inframill": map[string]string{"version": tc.core},
				"drivers": []map[string]string{{"name": "opentofu", "version": "0.2.0"}, {"name": "ansible", "version": "0.2.0"}},
			})
			data, err := Bundle(root, tc.selected)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				original, err := os.ReadFile(filepath.Join(root, "releases", tc.selected+".lock.json"))
				if err != nil || string(data) != string(original) {
					t.Fatalf("Bundle must return exact release bytes for publication: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReleaseRejectsObsoleteFieldsAndInvalidDrivers(t *testing.T) {
	for _, tc := range []struct {
		name, extra, drivers, suffix, want string
	}{
		{"obsolete version", `,"kit_version":"0.6.0"`, `[{"name":"opentofu","version":"0.2.0"},{"name":"ansible","version":"0.2.0"}]`, "", "unknown field"},
		{"duplicate driver", "", `[{"name":"opentofu","version":"0.2.0"},{"name":"opentofu","version":"0.2.0"}]`, "", "duplicate Kit driver"},
		{"missing driver", "", `[]`, "", "compiled driver catalog"},
		{"extra document", "", `[]`, `{}`, "one JSON release document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "releases"), 0755); err != nil {
				t.Fatal(err)
			}
			data := `{"version":"0.6.0","inframill":{"version":"0.6.0"},"drivers":` + tc.drivers + tc.extra + `}` + tc.suffix
			if err := os.WriteFile(filepath.Join(root, "releases/0.6.0.lock.json"), []byte(data), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := Bundle(root, "0.6.0"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReleaseLockSchemas(t *testing.T) {
	paths, err := filepath.Glob("../../releases/*.lock.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("missing release locks: %v", err)
	}
	for _, path := range paths {
		version := strings.TrimSuffix(filepath.Base(path), ".lock.json")
		if _, err := readKitRelease("../..", version); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}
