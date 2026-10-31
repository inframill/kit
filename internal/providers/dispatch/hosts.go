package dispatch

import (
	"errors"
	"net"
	"slices"

	"github.com/inframill/kit/internal/artifacts"
)

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

func (p *execution) hosts() (map[string]string, error) {
	result := map[string]string{}
	nodes, err := artifacts.ReadNodes(p.scope.ArtifactDir, p.static.Artifact, artifacts.Scope(p.scope.ArtifactDir), p.static.ProducerID, p.static.ProducerVersion)
	if err != nil {
		return nil, err
	}
	base := p.settings.Directories[p.args.Directory[0]].Select
	for _, name := range base.Names {
		if !slices.ContainsFunc(nodes.Nodes, func(n artifacts.Node) bool { return n.Name == name }) {
			return nil, errors.New("unknown selected node name")
		}
	}
	for _, role := range base.Roles {
		if !slices.ContainsFunc(nodes.Nodes, func(n artifacts.Node) bool { return slices.Contains(n.Roles, role) }) {
			return nil, errors.New("unknown selected node role")
		}
	}
	addresses := map[string]bool{}
	for _, node := range nodes.Nodes {
		if !base.matches(node) {
			continue
		}
		address := map[string]string{"public_ipv4": node.PublicIPv4, "private_ipv4": node.PrivateIPv4, "fqdn": node.FQDN}[p.settings.AddressMode]
		if address == "" || addresses[address] {
			return nil, errors.New("missing or duplicate selected address")
		}
		addresses[address] = true
		if p.static.Connection == "local" && !net.ParseIP(address).IsLoopback() {
			return nil, errors.New("local connections require loopback addresses")
		}
		result[node.Name] = address
	}
	if len(result) == 0 {
		return nil, errors.New("node selectors matched no hosts")
	}
	return result, nil
}
