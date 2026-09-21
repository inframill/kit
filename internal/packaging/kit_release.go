package packaging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/inframill/kit/internal/catalog"
)

type KitRelease struct {
	Version             string            `json:"version"`
	Drivers             []catalog.Release `json:"drivers"`
	Platforms           []string          `json:"platforms"`
	BaseImage           string            `json:"base_image"`
	BasePlatformDigests map[string]string `json:"base_platform_digests"`
	SourceDateEpoch     int64             `json:"source_date_epoch"`
	GoVersion           string            `json:"go_version"`
	Inframill           struct {
		Version  string `json:"version"`
		Revision string `json:"revision"`
		Binaries map[string]struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"binaries"`
	} `json:"inframill"`
	Blueprints         []BlueprintRelease `json:"blueprints"`
	OpenTofu           json.RawMessage    `json:"opentofu"`
	AnsibleCore        string             `json:"ansible_core"`
	Python             string             `json:"python"`
	PythonDependencies json.RawMessage    `json:"python_dependencies"`
	DebianSnapshot     string             `json:"debian_snapshot"`
	raw                []byte
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
	data, err := os.ReadFile(filepath.Join(root, "releases", version+".lock.json"))
	if err != nil {
		return release, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&release); err != nil {
		return release, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return release, errors.New("expected one JSON release document")
	}
	release.raw = data
	if release.Version != version {
		return release, errors.New("Kit release identity differs from selected version")
	}
	if !catalog.ValidVersion(release.Inframill.Version) {
		return release, errors.New("exact Inframill SemVer required in Kit release")
	}
	kitParts := strings.Split(version, ".")
	coreParts := strings.Split(release.Inframill.Version, ".")
	if kitParts[0] != coreParts[0] || kitParts[1] != coreParts[1] {
		return release, fmt.Errorf("Kit %s must match Inframill %s major/minor version", version, release.Inframill.Version)
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
	return release.Inframill.Version, err
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
	release, err := Kit(root, version)
	if err != nil {
		return nil, err
	}
	return release.raw, nil
}
