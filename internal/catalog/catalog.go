// Package catalog identifies independently versioned driver implementations.
package catalog

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	descriptors "github.com/inframill/kit"
	"github.com/inframill/kit/internal/drivers/ansible"
	"github.com/inframill/kit/internal/drivers/opentofu"
	driver "github.com/sir-hassan/inframill/pkg/driver"
)

type Release struct {
	Name           string   `json:"name" yaml:"name"`
	Version        string   `json:"version" yaml:"version"`
	ToolVersion    string   `json:"tool_version" yaml:"tool_version"`
	Protocol       int      `json:"protocol" yaml:"protocol"`
	Platforms      []string `json:"platforms" yaml:"platforms"`
	ArtifactSchema int      `json:"artifact_schema" yaml:"artifact_schema"`
	Status         string   `json:"status" yaml:"status"`
}

type Factory func(Release, string) driver.Driver

var factories = map[string]Factory{}
var releases = map[string]Release{}

func New(name, version, tool string) (driver.Driver, error) {
	r, err := Get(name, version)
	if err != nil {
		return nil, err
	}
	f, ok := factories[name+"@"+version]
	if !ok {
		return nil, fmt.Errorf("missing driver implementation")
	}
	return f(r, tool), nil
}

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
var name = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func ValidName(s string) bool { return name.MatchString(s) }
func ValidVersion(s string) bool {
	if !semver.MatchString(s) {
		return false
	}
	if _, pre, ok := strings.Cut(s, "-"); ok {
		for _, part := range strings.Split(pre, ".") {
			numeric := true
			for _, c := range part {
				if c < '0' || c > '9' {
					numeric = false
					break
				}
			}
			if numeric && len(part) > 1 && part[0] == '0' {
				return false
			}
		}
	}
	return true
}
func Register(r Release) {
	if !ValidName(r.Name) || !ValidVersion(r.Version) || r.ToolVersion == "" {
		panic("invalid driver release")
	}
	key := r.Name + "@" + r.Version
	if _, ok := releases[key]; ok {
		panic("duplicate driver release")
	}
	releases[key] = r
}
func Get(name, version string) (Release, error) {
	r, ok := releases[name+"@"+version]
	if !ok {
		return r, fmt.Errorf("driver %s@%s is not included; select an exact installed version", name, version)
	}
	return r, nil
}
func List() []Release {
	out := make([]Release, 0, len(releases))
	for _, r := range releases {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name+out[i].Version < out[j].Name+out[j].Version })
	return out
}
func init() {
	implementations := map[string]Factory{
		"opentofu@0.2.0": func(r Release, tool string) driver.Driver {
			return opentofu.Driver{Version: r.Version, ToolVersion: r.ToolVersion, Tool: tool}
		},
		"ansible@0.2.0": func(r Release, tool string) driver.Driver {
			return ansible.Driver{Version: r.Version, ToolVersion: r.ToolVersion, Tool: tool}
		},
	}
	data, err := descriptors.Release()
	if err != nil {
		panic(err)
	}
	var selected struct {
		Drivers []Release `json:"drivers"`
	}
	if err := json.Unmarshal(data, &selected); err != nil {
		panic(err)
	}
	for _, release := range selected.Drivers {
		key := release.Name + "@" + release.Version
		factory, ok := implementations[key]
		if !ok {
			panic("release driver has no implementation: " + key)
		}
		Register(release)
		factories[key] = factory
	}
}

// ConfiguredID encodes prereleases without collisions between dots and hyphens.
func ConfiguredID(name, version string) string {
	base, pre, hasPre := strings.Cut(version, "-")
	id := name + "-" + strings.ReplaceAll(base, ".", "-")
	if hasPre {
		id += "-pre-" + hex.EncodeToString([]byte(pre))
	}
	return id
}
