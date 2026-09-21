package packaging

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestEngineVersionUsesSelectedDescriptor(t *testing.T) {
	root := t.TempDir()
	script, err := os.ReadFile("../../scripts/read-version.sh")
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		"scripts/read-version.sh":  string(script),
		"releases/2.9.3.lock.json": `{"version":"2.9.3","inframill":{"version":"2.9.7-rc.1"}}`,
		"releases/3.1.2.lock.json": `{"version":"3.1.2","inframill":{"version":"3.1.0"}}`,
	} {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"VERSION", "KIT_VERSION", "BUNDLE_VERSION", "INFRAMILL_VERSION"} {
		t.Setenv(key, "9.9.9")
	}
	for kit, engine := range map[string]string{"2.9.3": "2.9.7-rc.1", "3.1.2": "3.1.0", "4.0.0": ""} {
		if err := os.WriteFile(filepath.Join(root, "VERSION"), fmt.Appendf(nil, "%s\n", kit), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := EngineVersion(root)
		if engine == "" {
			if err == nil {
				t.Fatal("missing selected descriptor accepted")
			}
		} else if err != nil || got != engine {
			t.Fatalf("EngineVersion = %q, %v; want %q", got, err, engine)
		}
	}
}

func TestProjectVersionUsesFileOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../scripts/read-version.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts/read-version.sh"), script, 0644); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"VERSION", "KIT_VERSION", "BUNDLE_VERSION"} {
		t.Setenv(key, "9.9.9")
	}
	for _, value := range []string{"0.4.0", "0.4.1-rc.1"} {
		if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte(value+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := ProjectVersion(root)
		if err != nil || got != value {
			t.Fatalf("ProjectVersion = %q, %v; want %q", got, err, value)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("v0.4.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectVersion(root); err == nil {
		t.Fatal("malformed VERSION accepted")
	}
}
