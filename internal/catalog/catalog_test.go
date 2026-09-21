package catalog

import (
	"encoding/json"
	"testing"

	descriptors "github.com/inframill/kit"
)

func TestSelectedReleaseDriversHaveImplementations(t *testing.T) {
	data, err := descriptors.Release()
	if err != nil {
		t.Fatal(err)
	}
	var release struct {
		Drivers []Release `json:"drivers"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		t.Fatal(err)
	}
	if len(release.Drivers) == 0 {
		t.Fatal("empty release catalog")
	}
	for _, d := range release.Drivers {
		if _, err := New(d.Name, d.Version, "/unused"); err != nil {
			t.Fatal(err)
		}
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
