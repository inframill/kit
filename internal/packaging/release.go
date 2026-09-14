package packaging

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/inframill/kit/internal/catalog"
	"github.com/opencontainers/go-digest"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type Requirement struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
}
type BlueprintRelease struct {
	SourceSHA256  string        `yaml:"source_sha256" json:"source_sha256"`
	Name          string        `yaml:"name" json:"name"`
	Version       string        `yaml:"version" json:"version"`
	Protocol      int           `yaml:"protocol" json:"protocol"`
	Platforms     []string      `yaml:"platforms" json:"platforms"`
	Architectures []string      `yaml:"architectures" json:"architectures"`
	Drivers       []Requirement `yaml:"drivers" json:"drivers"`
	Status        string        `yaml:"status" json:"status"`
}

func ReadYAML(path string, v any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if e = d.Decode(v); e != nil {
		return e
	}
	if e = d.Decode(new(any)); !errors.Is(e, io.EOF) {
		return errors.New("expected one YAML document")
	}
	return nil
}
func Blueprint(root, name, version string) (BlueprintRelease, string, error) {
	var r BlueprintRelease
	if !catalog.ValidName(name) || !catalog.ValidVersion(version) {
		return r, "", errors.New("exact blueprint name and SemVer required")
	}
	if e := ReadYAML(filepath.Join(root, "releases", "blueprints", name, version+".yaml"), &r); e != nil {
		return r, "", e
	}
	if r.Name != name || r.Version != version || r.Protocol != 1 {
		return r, "", errors.New("release identity or protocol mismatch")
	}
	sort.Strings(r.Platforms)
	sort.Strings(r.Architectures)
	if fmt.Sprint(r.Platforms) != "[linux/amd64 linux/arm64]" || fmt.Sprint(r.Architectures) != "[amd64 arm64]" {
		return r, "", errors.New("release must support ARM64 and AMD64")
	}
	src := filepath.Join(root, "blueprints", name, "versions", version)
	if e := validateSchema(filepath.Join(src, "schema.json")); e != nil {
		return r, "", e
	}
	var manifest struct {
		API         string                    `yaml:"api_version"`
		Description string                    `yaml:"description"`
		Drivers     map[string]map[string]any `yaml:"drivers"`
	}
	if e := ReadYAML(filepath.Join(src, "manifest.yaml"), &manifest); e != nil {
		return r, "", e
	}
	if manifest.API != "inframill.dev/blueprint/v1" || len(manifest.Drivers) != len(r.Drivers) || len(r.Drivers) == 0 {
		return r, "", errors.New("invalid blueprint manifest")
	}
	for _, d := range r.Drivers {
		if _, e := catalog.Get(d.Name, d.Version); e != nil {
			return r, "", e
		}
		c, ok := manifest.Drivers[d.ID]
		if !ok || c["id"] != d.ID || c["required_driver_version"] != d.Version {
			return r, "", errors.New("manifest and release driver requirements disagree")
		}
		if st, e := os.Stat(filepath.Join(src, "template", d.ID)); e != nil || !st.IsDir() {
			return r, "", errors.New("missing driver template directory")
		}
	}
	layer, e := Archive(src)
	if e != nil {
		return r, "", e
	}
	if r.SourceSHA256 != digest.FromBytes(layer).String() {
		return r, "", errors.New("blueprint source differs from release descriptor; seal the intended source before packaging")
	}
	return r, src, nil
}
