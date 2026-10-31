// Package catalog selects independently versioned provider implementations.
package catalog

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/inframill/inframill/pkg/runmill"
	ansible "github.com/inframill/kit/internal/providers/ansible"
	dispatch "github.com/inframill/kit/internal/providers/dispatch"
	opentofu "github.com/inframill/kit/internal/providers/opentofu"
)

type Release struct {
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	ToolVersion    string   `json:"tool_version,omitempty"`
	Protocol       int      `json:"protocol"`
	Platforms      []string `json:"platforms"`
	ArtifactSchema int      `json:"artifact_schema"`
	Status         string   `json:"status"`
}

func (r Release) Selector() string { return r.Name + "-" + r.Version }

type factory func(Release, string) runmill.Provider

type implementation struct {
	metadata []byte
	new      factory
}

type entry struct {
	release Release
	new     factory
}

type Catalog struct{ entries map[string]entry }

// testImplementations is populated only by the testproviders build tag.
var testImplementations []implementation

func Builtin() (*Catalog, error) {
	implementations := []implementation{
		{ansible.ReleaseJSON, func(r Release, tool string) runmill.Provider {
			return ansible.Provider{Version: r.Version, ToolVersion: r.ToolVersion, Tool: tool}
		}},
		{opentofu.ReleaseJSON, func(r Release, tool string) runmill.Provider {
			return opentofu.Provider{Version: r.Version, ToolVersion: r.ToolVersion, Tool: tool}
		}},
		{dispatch.ReleaseJSON, func(r Release, tool string) runmill.Provider {
			return dispatch.Provider{Version: r.Version, Tool: tool}
		}},
	}
	return newCatalog(append(implementations, testImplementations...))
}

func newCatalog(implementations []implementation) (*Catalog, error) {
	c := &Catalog{entries: make(map[string]entry, len(implementations))}
	for _, implementation := range implementations {
		var r Release
		if err := json.Unmarshal(implementation.metadata, &r); err != nil {
			return nil, fmt.Errorf("provider release metadata: %w", err)
		}
		if r.Name == "" || r.Version == "" || implementation.new == nil {
			return nil, fmt.Errorf("incomplete provider registration: %s", r.Selector())
		}
		if _, exists := c.entries[r.Selector()]; exists {
			return nil, fmt.Errorf("duplicate provider: %s", r.Selector())
		}
		c.entries[r.Selector()] = entry{r, implementation.new}
	}
	return c, nil
}

func (c *Catalog) Get(selector string) (Release, error) {
	e, ok := c.entries[selector]
	if !ok {
		return Release{}, fmt.Errorf("provider %q is not included; use 'exec list' to see available versions", selector)
	}
	r := e.release
	r.Platforms = append([]string(nil), r.Platforms...)
	return r, nil
}

func (c *Catalog) Provider(selector, tool string) (runmill.Provider, error) {
	r, err := c.Get(selector)
	if err != nil {
		return nil, err
	}
	return c.entries[selector].new(r, tool), nil
}

func (c *Catalog) List() []Release {
	keys := make([]string, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Release, 0, len(keys))
	for _, key := range keys {
		r, _ := c.Get(key)
		out = append(out, r)
	}
	return out
}
