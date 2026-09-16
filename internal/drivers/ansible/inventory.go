package ansible

import (
	"errors"
	"net"
	"sort"

	"github.com/inframill/kit/internal/artifacts"
)

type inventoryDocument struct {
	All inventoryGroup `json:"all"`
}
type inventoryGroup struct {
	Hosts    map[string]host           `json:"hosts,omitempty"`
	Children map[string]inventoryGroup `json:"children,omitempty"`
}
type host struct {
	Address      string `json:"ansible_host,omitempty"`
	User         string `json:"ansible_user,omitempty"`
	Connection   string `json:"ansible_connection,omitempty"`
	Architecture string `json:"kit_architecture,omitempty"`
}

func (p *prepared) inventory() (inventoryDocument, artifacts.Nodes, error) {
	result := inventoryDocument{All: inventoryGroup{Hosts: map[string]host{}, Children: map[string]inventoryGroup{}}}
	nodes, err := artifacts.ReadNodes(p.scope.ArtifactDirectory, p.manifest.Artifact, p.scope.Binding, p.manifest.ProducerID, p.manifest.ProducerVersion)
	if err != nil {
		return result, nodes, err
	}
	roles := map[string]bool{}
	for _, role := range p.manifest.Roles {
		roles[role] = true
	}
	addresses := map[string]bool{}
	for _, node := range nodes.Nodes {
		address := ""
		switch p.settings.AddressMode {
		case "public_ipv4":
			address = node.PublicIPv4
		case "private_ipv4":
			address = node.PrivateIPv4
		case "fqdn":
			address = node.FQDN
		}
		if address == "" || addresses[address] {
			return result, nodes, errors.New("missing or duplicate selected address")
		}
		addresses[address] = true
		if p.manifest.Connection == "local" && !net.ParseIP(address).IsLoopback() {
			return result, nodes, errors.New("local connections require loopback addresses")
		}
		result.All.Hosts[node.Name] = host{address, p.settings.SSHUser, p.manifest.Connection, node.Architecture}
		for _, role := range node.Roles {
			if !roles[role] {
				return result, nodes, errors.New("undeclared node role")
			}
			group := result.All.Children[role]
			if group.Hosts == nil {
				group.Hosts = map[string]host{}
			}
			group.Hosts[node.Name] = host{}
			result.All.Children[role] = group
		}
	}
	for _, name := range p.args.Hosts {
		if _, ok := result.All.Hosts[name]; !ok {
			return result, nodes, errors.New("unknown selected host")
		}
	}
	return result, nodes, nil
}
func (p *prepared) expected(nodes artifacts.Nodes) []string {
	out := append([]string(nil), p.args.Hosts...)
	if len(out) == 0 {
		for _, n := range nodes.Nodes {
			out = append(out, n.Name)
		}
	}
	sort.Strings(out)
	return out
}
