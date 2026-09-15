// Package catalog identifies independently versioned driver implementations.
package catalog

import (
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	descriptors "github.com/inframill/kit/releases"
	"gopkg.in/yaml.v3"
)

type Release struct {
	Name        string `json:"name" yaml:"name"`
	Version     string `json:"version" yaml:"version"`
	ToolVersion string `json:"tool_version" yaml:"tool_version"`
}

var releases = map[string]Release{}
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
	files, err := fs.Glob(descriptors.Drivers, "drivers/*/*.yaml")
	if err != nil {
		panic(err)
	}
	for _, path := range files {
		data, err := descriptors.Drivers.ReadFile(path)
		if err != nil {
			panic(err)
		}
		var r Release
		if err = yaml.Unmarshal(data, &r); err != nil {
			panic(err)
		}
		Register(r)
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
