package packaging

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/inframill/kit/internal/catalog"
)

type KitRelease struct {
	Version          string            `yaml:"version"`
	InframillVersion string            `yaml:"inframill_version"`
	Drivers          []catalog.Release `yaml:"drivers"`
}

// ProjectVersion reads the authoritative VERSION file using the shared validator.
// Build selectors and environment variables never override the file.
func ProjectVersion(root string) (string, error) {
	cmd := exec.Command("sh", "scripts/read-version.sh")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read Kit VERSION: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSuffix(string(output), "\n"), nil
}

// readKitRelease validates identity and the explicitly declared engine target.
func readKitRelease(root, version string) (KitRelease, error) {
	var release KitRelease
	if !catalog.ValidVersion(version) {
		return release, errors.New("exact Kit SemVer required")
	}
	if err := ReadYAML(filepath.Join(root, "releases/kits", version+".yaml"), &release); err != nil {
		return release, err
	}
	if release.Version != version {
		return release, errors.New("Kit release identity differs from selected version")
	}
	if !catalog.ValidVersion(release.InframillVersion) {
		return release, errors.New("exact Inframill SemVer required in Kit release")
	}
	kitParts := strings.Split(version, ".")
	coreParts := strings.Split(release.InframillVersion, ".")
	if kitParts[0] != coreParts[0] || kitParts[1] != coreParts[1] {
		return release, fmt.Errorf("Kit %s must match Inframill %s major/minor version", version, release.InframillVersion)
	}
	return release, nil
}

// EngineVersion uses the Kit release selected by VERSION, never the sibling checkout.
// Blueprint fixtures may have their own source root and extra test drivers.
func EngineVersion(root string) (string, error) {
	version, err := ProjectVersion(root)
	if err != nil {
		return "", err
	}
	release, err := readKitRelease(root, version)
	return release.InframillVersion, err
}

// Kit validates the declared engine target and compiled driver catalog.
func Kit(root, version string) (KitRelease, error) {
	release, err := readKitRelease(root, version)
	if err != nil {
		return release, err
	}
	if len(release.Drivers) != len(catalog.List()) {
		return release, errors.New("Kit descriptor differs from compiled driver catalog")
	}
	seen := map[string]bool{}
	for _, d := range release.Drivers {
		key := d.Name + "@" + d.Version
		if seen[key] {
			return release, errors.New("duplicate Kit driver requirement")
		}
		seen[key] = true
		if _, err := catalog.Get(d.Name, d.Version); err != nil {
			return release, err
		}
	}
	return release, nil
}

// Bundle validates the image identity before build, native copy or publication.
// It returns the original lock bytes so publication can verify the build's pin.
func Bundle(root, version string) ([]byte, error) {
	if !catalog.ValidVersion(version) {
		return nil, errors.New("exact bundle SemVer required")
	}
	data, err := os.ReadFile(filepath.Join(root, "releases/bundles", version+".lock.yaml"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		Version    string `json:"version"`
		KitVersion string `json:"kit_version"`
		Inframill  struct {
			Version string `json:"version"`
		} `json:"inframill"`
	}
	if err = json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	if lock.Version != version || lock.KitVersion != version {
		return nil, errors.New("bundle, Kit and selected release versions must match exactly")
	}
	kit, err := Kit(root, lock.KitVersion)
	if err != nil {
		return nil, err
	}
	if lock.Inframill.Version != kit.InframillVersion {
		return nil, errors.New("bundle Inframill version differs from Kit target")
	}
	return data, nil
}
