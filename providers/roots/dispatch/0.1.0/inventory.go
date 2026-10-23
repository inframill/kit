package dispatch

import (
	"errors"
	"net"
	"slices"
	"sort"

	"github.com/inframill/kit/providers/roots/internal/artifacts"
)

type inventoryDocument struct {
	All inventoryGroup `json:"all"`
}
type inventoryGroup struct {
	Hosts map[string]host `json:"hosts"`
}
type host struct {
	Address      string `json:"ansible_host"`
	User         string `json:"ansible_user"`
	Connection   string `json:"ansible_connection"`
	Architecture string `json:"kit_architecture"`
}

func (s Selector) matches(n artifacts.Node) bool {
	if len(s.Names) > 0 && !slices.Contains(s.Names, n.Name) || s.Architecture != "" && s.Architecture != n.Architecture {
		return false
	}
	if len(s.Roles) > 0 {
		for _, role := range s.Roles {
			if slices.Contains(n.Roles, role) {
				return true
			}
		}
		return false
	}
	return true
}

func (p *prepared) inventory() (inventoryDocument, []string, error) {
	result := inventoryDocument{All: inventoryGroup{Hosts: map[string]host{}}}
	nodes, err := artifacts.ReadNodes(p.scope.ArtifactDir, p.static.Artifact, artifacts.Scope(p.scope.ArtifactDir), p.static.ProducerID, p.static.ProducerVersion)
	if err != nil {
		return result, nil, err
	}
	base := p.settings.Directories[p.args.Directory].Select
	for _, name := range base.Names {
		if !slices.ContainsFunc(nodes.Nodes, func(n artifacts.Node) bool { return n.Name == name }) {
			return result, nil, errors.New("unknown selected node name")
		}
	}
	for _, role := range base.Roles {
		if !slices.ContainsFunc(nodes.Nodes, func(n artifacts.Node) bool { return slices.Contains(n.Roles, role) }) {
			return result, nil, errors.New("unknown selected node role")
		}
	}
	addresses := map[string]bool{}
	var selected []string
	for _, node := range nodes.Nodes {
		if !base.matches(node) {
			continue
		}
		address := map[string]string{"public_ipv4": node.PublicIPv4, "private_ipv4": node.PrivateIPv4, "fqdn": node.FQDN}[p.settings.AddressMode]
		if address == "" || addresses[address] {
			return result, nil, errors.New("missing or duplicate selected address")
		}
		addresses[address] = true
		if p.static.Connection == "local" && !net.ParseIP(address).IsLoopback() {
			return result, nil, errors.New("local connections require loopback addresses")
		}
		result.All.Hosts[node.Name] = host{address, p.settings.SSHUser, p.static.Connection, node.Architecture}
		selected = append(selected, node.Name)
	}
	if len(selected) == 0 {
		return result, nil, errors.New("node selectors matched no hosts")
	}
	sort.Strings(selected)
	return result, selected, nil
}
