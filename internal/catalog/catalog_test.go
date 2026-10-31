package catalog

import (
	"testing"

	"github.com/inframill/inframill/pkg/runmill"
)

func TestBuiltinRegistry(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	releases := c.List()
	if len(releases) != 3 {
		t.Fatalf("provider count = %d", len(releases))
	}
	for _, r := range releases {
		if r.Version == "" || r.Protocol != 1 {
			t.Fatalf("invalid release: %+v", r)
		}
		provider, err := c.Provider(r.Selector(), "/unused")
		if err != nil {
			t.Fatal(err)
		}
		definition, err := provider.Definition()
		if err != nil || definition.Name != r.Name {
			t.Fatalf("provider metadata: %+v, %v", definition, err)
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
