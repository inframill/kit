package catalog

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/sir-hassan/inframill/pkg/runmill"
)

func TestRegistryMatchesReleaseSelections(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../releases.json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct{ Providers []Release }
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if len(c.List()) != len(config.Providers) {
		t.Fatal("compiled registry differs from releases.json")
	}
	for _, r := range config.Providers {
		provider, err := c.Provider(r.Selector(), "/unused")
		if err != nil {
			t.Fatal(err)
		}
		definition, err := provider.Definition()
		if err != nil || definition.Name != r.Name {
			t.Fatal(definition, err)
		}
	}
}

func TestSelectorsAreExactAndSupportHyphens(t *testing.T) {
	newProvider := func(Release, string) runmill.Provider { return nil }
	c, err := newCatalog([]implementation{
		{[]byte(`{"name":"my-provider","version":"1.2.3-rc.1"}`), newProvider},
		{[]byte(`{"name":"my-provider","version":"1.2.3-rc-1"}`), newProvider},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"my-provider-1.2.3-rc.1", "my-provider-1.2.3-rc-1"} {
		if _, err := c.Get(selector); err != nil {
			t.Fatal(err)
		}
	}
	for _, selector := range []string{"my-provider", "my-provider-1.2", "my-provider-latest", "my-provider-1.2.3"} {
		if _, err := c.Get(selector); err == nil {
			t.Fatal("accepted unavailable selector", selector)
		}
	}
	_, err = newCatalog([]implementation{
		{[]byte(`{"name":"my-provider","version":"1.2.3"}`), newProvider},
		{[]byte(`{"name":"my-provider","version":"1.2.3"}`), newProvider},
	})
	if err == nil {
		t.Fatal("accepted duplicate provider")
	}
}
