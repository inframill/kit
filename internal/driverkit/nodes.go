package driverkit

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"

	wire "github.com/sir-hassan/inframill/pkg/driver"
)

type Node struct {
	Name         string   `json:"name"`
	Roles        []string `json:"roles"`
	Architecture string   `json:"architecture"`
	PublicIPv4   string   `json:"public_ipv4"`
	PrivateIPv4  string   `json:"private_ipv4"`
	FQDN         string   `json:"fqdn,omitempty"`
}
type Nodes struct {
	Schema          int          `json:"schema"`
	Valid           bool         `json:"valid"`
	Binding         wire.Binding `json:"binding"`
	ProducerID      string       `json:"producer_id"`
	ProducerName    string       `json:"producer_name"`
	ProducerVersion string       `json:"producer_version"`
	CommandID       string       `json:"command_id"`
	Nodes           []Node       `json:"nodes"`
}

func checkNode(n Node) error {
	if !identifier.MatchString(n.Name) || n.Architecture != "arm64" && n.Architecture != "amd64" || len(n.Roles) == 0 {
		return errors.New("invalid node identity, architecture or roles")
	}
	seen := map[string]bool{}
	for _, r := range n.Roles {
		if !identifier.MatchString(r) || r == "all" || r == "ungrouped" || seen[r] {
			return errors.New("invalid or duplicate role")
		}
		seen[r] = true
	}
	for _, a := range []string{n.PublicIPv4, n.PrivateIPv4} {
		if a != "" && (net.ParseIP(a) == nil || net.ParseIP(a).To4() == nil) {
			return errors.New("invalid IPv4 output")
		}
	}
	if n.FQDN != "" && !regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9-]*\.)+[a-zA-Z0-9][a-zA-Z0-9-]*$`).MatchString(n.FQDN) {
		return errors.New("invalid FQDN")
	}
	return nil
}
func loadNodes(c Config, req invocation) (Nodes, error) {
	var n Nodes
	b, e := readPrivate(req.ArtifactDirectory, c.Artifact, maxArtifact)
	if e != nil {
		return n, e
	}
	if e = decode(b, &n); e != nil {
		return n, e
	}
	if n.Schema != 1 || !n.Valid || n.Binding != req.Binding || n.ProducerID != c.ProducerID || n.ProducerName != "opentofu" || n.ProducerVersion != c.ProducerVersion || !uuid.MatchString(n.CommandID) || len(n.Nodes) == 0 {
		return n, errors.New("missing, invalid, incompatible or incorrectly bound node artifact")
	}
	return n, nil
}
func inventory(c Config, req invocation) (map[string]any, error) {
	ns, e := loadNodes(c, req)
	if e != nil {
		return nil, e
	}
	mode, e := c.AddressMode.resolve(req.Values)
	if e != nil {
		return nil, e
	}
	user, e := c.SSHUser.resolve(req.Values)
	if e != nil {
		return nil, e
	}
	hosts := map[string]any{}
	groups := map[string][]string{}
	addresses := map[string]bool{}
	roles := map[string]bool{}
	for _, r := range c.Roles {
		roles[r] = true
	}
	for _, n := range ns.Nodes {
		if e = checkNode(n); e != nil {
			return nil, e
		}
		if _, exists := hosts[n.Name]; exists {
			return nil, errors.New("duplicate host")
		}
		addr := ""
		switch mode {
		case "public_ipv4":
			addr = n.PublicIPv4
		case "private_ipv4":
			addr = n.PrivateIPv4
		case "fqdn":
			addr = n.FQDN
		default:
			return nil, errors.New("invalid address mode")
		}
		if addr == "" || addresses[addr] {
			return nil, errors.New("missing or duplicate selected host address")
		}
		addresses[addr] = true
		if c.Connection == "local" && (!net.ParseIP(addr).IsLoopback()) {
			return nil, errors.New("local connection only supports loopback fixtures")
		}
		hosts[n.Name] = map[string]any{"ansible_host": addr, "ansible_user": user, "ansible_connection": c.Connection, "kit_architecture": n.Architecture}
		for _, role := range n.Roles {
			if !roles[role] {
				return nil, fmt.Errorf("undeclared role %s", role)
			}
			groups[role] = append(groups[role], n.Name)
		}
	}
	children := map[string]any{}
	for role, ns := range groups {
		sort.Strings(ns)
		m := map[string]any{}
		for _, n := range ns {
			m[n] = map[string]any{}
		}
		children[role] = map[string]any{"hosts": m}
	}
	inv := map[string]any{"all": map[string]any{"hosts": hosts, "children": children}}
	if e = writePrivate(req.WorkspaceDirectory, "kit-inventory.json", inv); e != nil {
		return nil, e
	}
	return map[string]any{"inventory": "kit-inventory.json", "hosts": len(hosts), "producer_command_id": ns.CommandID}, nil
}
