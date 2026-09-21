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
)

type DriverSelection struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type KitRelease struct {
	Version             string            `json:"version"`
	Drivers             []DriverSelection `json:"drivers"`
	Platforms           []string          `json:"platforms"`
	BaseImage           string            `json:"base_image"`
	BasePlatformDigests map[string]string `json:"base_platform_digests"`
	SourceDateEpoch     int64             `json:"source_date_epoch"`
	Inframill           struct {
		Version  string `json:"version"`
		Revision string `json:"revision"`
		Binaries map[string]struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"binaries"`
	} `json:"inframill"`
	OpenTofu           json.RawMessage `json:"opentofu"`
	AnsibleCore        string          `json:"ansible_core"`
	Python             string          `json:"python"`
	PythonDependencies json.RawMessage `json:"python_dependencies"`
	DebianSnapshot     string          `json:"debian_snapshot"`
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
	if !ValidVersion(version) {
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
	if !ValidVersion(release.Inframill.Version) {
		return release, errors.New("exact Inframill SemVer required in Kit release")
	}
	kitParts := strings.Split(version, ".")
	coreParts := strings.Split(release.Inframill.Version, ".")
	if kitParts[0] != coreParts[0] || kitParts[1] != coreParts[1] {
		return release, fmt.Errorf("Kit %s must match Inframill %s major/minor version", version, release.Inframill.Version)
	}
	return release, nil
}

// Kit validates image metadata. Driver artifacts are verified by the image assembler.
func Kit(root, version string) (KitRelease, error) {
	release, err := readKitRelease(root, version)
	if err != nil {
		return release, err
	}
	if len(release.Drivers) == 0 {
		return release, errors.New("Kit release has no selected drivers")
	}
	seen := map[string]bool{}
	for _, d := range release.Drivers {
		key := d.Name + "@" + d.Version
		if seen[key] {
			return release, errors.New("duplicate Kit driver requirement")
		}
		seen[key] = true
		if !ValidName(d.Name) || !ValidVersion(d.Version) {
			return release, errors.New("invalid driver selection")
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
