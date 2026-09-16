package packaging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/inframill/kit/internal/artifacts"
	"github.com/inframill/kit/internal/catalog"
	"github.com/opencontainers/go-digest"
	"gopkg.in/yaml.v3"
)

type Requirement struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
}
type BlueprintRelease struct {
	InframillVersion string        `yaml:"inframill_version" json:"inframill_version"`
	SourceSHA256     string        `yaml:"source_sha256" json:"source_sha256"`
	Name             string        `yaml:"name" json:"name"`
	Version          string        `yaml:"version" json:"version"`
	Protocol         int           `yaml:"protocol" json:"protocol"`
	Platforms        []string      `yaml:"platforms" json:"platforms"`
	Architectures    []string      `yaml:"architectures" json:"architectures"`
	Drivers          []Requirement `yaml:"drivers" json:"drivers"`
	Status           string        `yaml:"status" json:"status"`
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
func Blueprint(root, name, version, engineVersion string) (BlueprintRelease, string, error) {
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
	if !catalog.ValidVersion(engineVersion) || r.InframillVersion != engineVersion {
		return r, "", errors.New("blueprint Inframill version differs from selected Kit target")
	}
	sort.Strings(r.Platforms)
	sort.Strings(r.Architectures)
	if fmt.Sprint(r.Platforms) != "[linux/amd64 linux/arm64]" || fmt.Sprint(r.Architectures) != "[amd64 arm64]" {
		return r, "", errors.New("release must support ARM64 and AMD64")
	}
	src := filepath.Join(root, "blueprints", name, "versions", version)
	data, e := artifacts.Read(src, Filename, MaxBytes)
	if e != nil {
		return r, "", e
	}
	definition, e := parseDefinition(data)
	if e != nil {
		return r, "", e
	}
	if e = validateSchema(definition.ValuesSchema); e != nil {
		return r, "", e
	}
	if len(definition.DriverSettings) != len(r.Drivers) {
		return r, "", errors.New("blueprint settings and release driver count differs")
	}
	for _, d := range r.Drivers {
		if _, e := catalog.Get(d.Name, d.Version); e != nil {
			return r, "", e
		}
		c, ok := definition.DriverSettings[d.ID]
		if !ok || c["id"] != d.ID || c["required_driver_version"] != d.Version {
			return r, "", errors.New("blueprint settings and release driver requirements disagree")
		}
		implementation, e := catalog.New(d.Name, d.Version, "")
		if e != nil {
			return r, "", e
		}
		if e = implementation.ValidateSettings(context.Background(), c); e != nil {
			return r, "", e
		}
		if raw, exists := c["settings_file"]; exists {
			settings, ok := raw.(string)
			if !ok || !artifacts.Flat(settings) {
				return r, "", errors.New("invalid settings file")
			}
			dir := filepath.Join(src, "template", d.ID)
			if _, e = artifacts.Read(dir, settings+".gotmpl", artifacts.SettingsLimit); e != nil {
				if _, e = artifacts.Read(dir, settings, artifacts.SettingsLimit); e != nil {
					return r, "", errors.New("missing settings template")
				}
			}
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
