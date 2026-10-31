package artifacts

import "testing"

func TestReservedInventoryNamesCannotBeNodes(t *testing.T) {
	for _, name := range []string{"all", "ungrouped", "common", "docker", "web", "data"} {
		node := Node{Name: name, Roles: []string{"docker"}, Architecture: "amd64", PublicIPv4: "192.0.2.1"}
		err := CheckNode(node)
		reserved := name == "all" || name == "ungrouped"
		if (err != nil) != reserved {
			t.Fatalf("name %q: %v", name, err)
		}
	}
}
