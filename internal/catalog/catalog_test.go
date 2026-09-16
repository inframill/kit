package catalog

import (
	"io/fs"
	"strings"
	"testing"

	descriptors "github.com/inframill/kit/releases"
)

func TestEveryDriverDescriptorHasAnImplementation(t *testing.T) {
	err := fs.WalkDir(descriptors.Drivers, "drivers", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		parts := strings.Split(path, "/")
		if len(parts) != 3 || !strings.HasSuffix(parts[2], ".yaml") {
			t.Errorf("unexpected driver descriptor: %s", path)
			return nil
		}
		if _, err := New(parts[1], strings.TrimSuffix(parts[2], ".yaml"), "/unused"); err != nil {
			t.Errorf("descriptor %s has no executable implementation: %v", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExactSemVerAndCollisionFreeIDs(t *testing.T) {
	for _, v := range []string{"1", "1.2", "v1.2.3", "01.2.3", "1.2.3-01", "1.2.3-", "latest"} {
		if ValidVersion(v) {
			t.Fatal("accepted " + v)
		}
	}
	for _, v := range []string{"0.1.0", "1.2.3", "1.2.3-rc.1"} {
		if !ValidVersion(v) {
			t.Fatal("rejected " + v)
		}
	}
	if ConfiguredID("tofu", "1.0.0-rc.1") == ConfiguredID("tofu", "1.0.0-rc-1") {
		t.Fatal("prerelease aliases collide")
	}
}
