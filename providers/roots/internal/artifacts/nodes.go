package artifacts

import (
	"errors"
	"net"
	"regexp"
)

var Identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var dns = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9-]*\.)+[a-zA-Z0-9][a-zA-Z0-9-]*$`)

type Node struct {
	Name         string   `json:"name"`
	Roles        []string `json:"roles"`
	Architecture string   `json:"architecture"`
	PublicIPv4   string   `json:"public_ipv4"`
	PrivateIPv4  string   `json:"private_ipv4"`
	FQDN         string   `json:"fqdn,omitempty"`
}
type Nodes struct {
	Schema          int    `json:"schema"`
	Valid           bool   `json:"valid"`
	ArtifactScope   string `json:"artifact_scope"`
	ProducerID      string `json:"producer_id"`
	ProducerVersion string `json:"producer_version"`
	ExecutionID     string `json:"execution_id"`
	Nodes           []Node `json:"nodes"`
}

func CheckNode(n Node) error {
	if !Identifier.MatchString(n.Name) || n.Architecture != "arm64" && n.Architecture != "amd64" || len(n.Roles) == 0 {
		return errors.New("invalid node identity or architecture")
	}
	seen := map[string]bool{}
	for _, role := range n.Roles {
		if !Identifier.MatchString(role) || role == "all" || role == "ungrouped" || seen[role] {
			return errors.New("invalid node role")
		}
		seen[role] = true
	}
	for _, address := range []string{n.PublicIPv4, n.PrivateIPv4} {
		if address != "" && (net.ParseIP(address) == nil || net.ParseIP(address).To4() == nil) {
			return errors.New("invalid IPv4 address")
		}
	}
	if n.FQDN != "" && !dns.MatchString(n.FQDN) {
		return errors.New("invalid FQDN")
	}
	return nil
}
func ReadNodes(directory, name string, scope string, producer, version string) (Nodes, error) {
	var n Nodes
	data, err := Read(directory, name, Limit)
	if err != nil {
		return n, err
	}
	if err = Decode(data, &n); err != nil {
		return n, err
	}
	if n.Schema != 1 || !n.Valid || n.ArtifactScope != scope || n.ProducerID != producer || n.ProducerVersion != version || !ValidExecutionID(n.ExecutionID) || len(n.Nodes) == 0 || len(n.Nodes) > 1000 {
		return n, errors.New("incompatible node artifact")
	}
	seen := map[string]bool{}
	for _, node := range n.Nodes {
		if err = CheckNode(node); err != nil {
			return n, err
		}
		if seen[node.Name] {
			return n, errors.New("duplicate node")
		}
		seen[node.Name] = true
	}
	return n, nil
}
