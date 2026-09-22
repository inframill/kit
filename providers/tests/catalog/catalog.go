// Package catalog identifies independently versioned provider implementations.
package catalog

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/inframill/kit/providers/roots/ansible/0.3.0"
	"github.com/inframill/kit/providers/roots/opentofu/0.3.0"
	"github.com/sir-hassan/inframill/pkg/runmill"
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

type Factory func(Release, string) runmill.Provider

var factories = map[string]Factory{}
var releases = map[string]Release{}

func New(name, version, tool string) (runmill.Provider, error) {
	r, err := Get(name, version)
	if err != nil {
		return nil, err
	}
	f, ok := factories[name+"@"+version]
	if !ok {
		return nil, fmt.Errorf("missing provider implementation")
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
		panic("invalid provider release")
	}
	key := r.Name + "@" + r.Version
	if _, ok := releases[key]; ok {
		panic("duplicate provider release")
	}
	releases[key] = r
}
func Get(name, version string) (Release, error) {
	r, ok := releases[name+"@"+version]
	if !ok {
		return r, fmt.Errorf("provider %s@%s is not included; select an exact installed version", name, version)
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
		"opentofu@0.3.0": func(r Release, tool string) runmill.Provider {
			return opentofu.Provider{Version: r.Version, ToolVersion: r.ToolVersion, Tool: tool}
		},
		"ansible@0.3.0": func(r Release, tool string) runmill.Provider {
			return ansible.Provider{Version: r.Version, ToolVersion: r.ToolVersion, Tool: tool}
		},
	}
	var selected struct{ Providers []Release }
	for _, data := range [][]byte{opentofu.ReleaseJSON, ansible.ReleaseJSON} {
		var release Release
		if err := json.Unmarshal(data, &release); err != nil {
			panic(err)
		}
		selected.Providers = append(selected.Providers, release)
	}
	for _, release := range selected.Providers {
		key := release.Name + "@" + release.Version
		factory, ok := implementations[key]
		if !ok {
			panic("release provider has no implementation: " + key)
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
