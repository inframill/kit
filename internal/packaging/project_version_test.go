package packaging

import (
	"os"
	"path/filepath"
	"testing"
)

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
