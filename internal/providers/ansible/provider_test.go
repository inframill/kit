package ansible

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inframill/kit/internal/artifacts"
	"github.com/inframill/kit/pkg/runmill"
)

func TestHostKeyPolicy(t *testing.T) {
	for _, policy := range []string{"", "accept-new", "strict"} {
		for _, command := range []string{"ping", "syntax-check", "check", "apply"} {
			t.Run(policy+"/"+command, func(t *testing.T) {
				d, s, n := fixture(t)
				s.Settings["id"] = "custom"
				s.Settings["connection"] = "ssh"
				s.ArtifactDir = filepath.Join(t.TempDir(), "instance's \"storage\"", "artifacts")
				if err := os.MkdirAll(s.ArtifactDir, 0700); err != nil {
					t.Fatal(err)
				}
				n.ArtifactScope = artifacts.Scope(s.ArtifactDir)
				delete(s.Settings, "host_key_policy")
				if policy != "" {
					s.Settings["host_key_policy"] = policy
				}
				if err := artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n); err != nil {
					t.Fatal(err)
				}
				d.Tool = filepath.Join(t.TempDir(), "ansible")
				script := `#!/bin/sh
if [ "$1" = --version ]; then
  printf '[core 2.20.1]'
  exit 0
fi
printf '%s\n' "$ANSIBLE_HOST_KEY_CHECKING" "$ANSIBLE_SSH_ARGS" > ssh-environment
printf '%s' '{"schema":1,"hosts":{"local":{"ok":1}}}'
`
				if err := os.WriteFile(d.Tool, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				args := runmill.Arguments{}
				if command == "syntax-check" || command == "check" {
					args["playbook"] = "site"
				}
				if _, err := d.Execute(t.Context(), s, command, args, nil); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(s.WorkspaceDir, "ssh-environment"))
				if err != nil {
					t.Fatal(err)
				}
				want := "yes"
				if policy == "accept-new" {
					want = "accept-new"
				}
				knownHosts := filepath.Join(s.ArtifactDir, "custom-known_hosts")
				wantEnvironment := fmt.Sprintf("True\n-o BatchMode=yes -o StrictHostKeyChecking=%s -o %q", want, fmt.Sprintf("UserKnownHostsFile=%q", knownHosts))
				if got := strings.TrimSpace(string(data)); got != wantEnvironment {
					t.Fatalf("unexpected SSH environment: %q", got)
				}
			})
		}
	}
	d, s, _ := fixture(t)
	s.Settings["host_key_policy"] = "invalid"
	if err := d.ValidateSettings(t.Context(), s.Settings); err == nil {
		t.Fatal("accepted invalid host-key policy")
	}
}

func TestApplyDefaultsToSite(t *testing.T) {
	d, scope, nodes := fixture(t)
	if err := artifacts.Write(scope.ArtifactDir, "tofu-nodes-v1.json", nodes); err != nil {
		t.Fatal(err)
	}
	scope.Settings["playbooks"] = map[string]string{"site": "site.yml", "other": "site.yml"}
	for _, tc := range []struct {
		args runmill.Arguments
		want string
	}{
		{nil, "site"},
		{runmill.Arguments{}, "site"},
		{runmill.Arguments{"hosts": []string{"local"}, "tags": []string{"setup"}}, "site"},
		{runmill.Arguments{"playbook": "other"}, "other"},
	} {
		p, err := d.loadExecution(t.Context(), scope, "apply", tc.args)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.args.Playbook; got != tc.want {
			t.Fatalf("playbook = %q, want %q", got, tc.want)
		}
	}
	for _, args := range []runmill.Arguments{{"playbook": ""}, {"playbook": nil}, {"playbook": "missing"}} {
		if _, err := d.Execute(t.Context(), scope, "apply", args, nil); err == nil {
			t.Fatalf("accepted invalid playbook %v", args)
		}
	}
	for _, command := range []string{"syntax-check", "check"} {
		if _, err := d.Execute(t.Context(), scope, command, nil, nil); err == nil {
			t.Fatalf("%s accepted a missing playbook", command)
		}
	}
	scope.Settings["playbooks"] = map[string]string{"other": "site.yml"}
	if _, err := d.Execute(t.Context(), scope, "apply", nil, nil); err == nil {
		t.Fatal("accepted an undeclared default site playbook")
	}
}

func TestPingPrerequisites(t *testing.T) {
	d, s, n := fixture(t)
	if _, err := d.Execute(t.Context(), s, "ping", nil, nil); err == nil {
		t.Fatal("ping accepted missing nodes")
	}
	artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n)
	os.Remove(filepath.Join(s.WorkspaceDir, "site.yml"))
	for _, args := range []runmill.Arguments{nil, {"hosts": []string{"local"}}} {
		if _, err := d.loadExecution(t.Context(), s, "ping", args); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range []runmill.Arguments{{"playbook": "site"}, {"tags": []string{}}, {"hosts": nil}, {"hosts": "local"}, {"hosts": []string{"missing"}}, {"hosts": []string{"local", "local"}}} {
		if _, err := d.Execute(t.Context(), s, "ping", args, nil); err == nil {
			t.Fatalf("accepted invalid ping arguments: %v", args)
		}
	}
}

func TestHostNamesCannotExpandToGroups(t *testing.T) {
	for _, name := range []string{"common", "docker", "web", "data"} {
		t.Run(name, func(t *testing.T) {
			d, scope, nodes := fixture(t)
			scope.Settings["roles"] = []string{"common", "docker", "web", "data"}
			role := "common"
			if name == role {
				role = "docker"
			}
			workerRole := name
			if name == "all" || name == "ungrouped" {
				workerRole = "web"
			}
			nodes.Nodes = []artifacts.Node{
				{Name: name, Roles: []string{role}, Architecture: "arm64", PublicIPv4: "127.0.0.1"},
				{Name: "worker", Roles: []string{workerRole}, Architecture: "arm64", PublicIPv4: "127.0.0.2"},
			}
			if err := artifacts.Write(scope.ArtifactDir, "tofu-nodes-v1.json", nodes); err != nil {
				t.Fatal(err)
			}
			recap, err := json.Marshal(Recap{Schema: 1, Hosts: map[string]Stats{name: {OK: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			d.Tool = filepath.Join(t.TempDir(), "ansible")
			script := `#!/bin/sh
if [ "$1" = --version ]; then printf '[core 2.20.1]'; exit 0; fi
while [ "$#" -gt 0 ]; do
  case "$1" in
    -i) cp "$2" used-inventory.json; shift;;
    --limit) exit 1;;
  esac
  shift
done
printf '%s' '` + string(recap) + "'\n"
			if err := os.WriteFile(d.Tool, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Execute(t.Context(), scope, "ping", runmill.Arguments{"hosts": []string{name}}, nil); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(scope.WorkspaceDir, "used-inventory.json"))
			if err != nil {
				t.Fatal(err)
			}
			var inventory inventoryDocument
			if err := json.Unmarshal(data, &inventory); err != nil {
				t.Fatal(err)
			}
			if len(inventory.All.Hosts) != 1 || inventory.All.Hosts[name].Address != "127.0.0.1" {
				t.Fatalf("host selection expanded: %s", data)
			}
			if len(inventory.All.Children) != 1 || len(inventory.All.Children[role].Hosts) != 1 {
				t.Fatalf("unselected role membership remains: %s", data)
			}
			if _, ok := inventory.All.Children[role].Hosts[name]; !ok {
				t.Fatalf("selected role membership lost: %s", data)
			}
		})
	}
}

func TestToolFailuresReturnWithDiagnostics(t *testing.T) {
	for _, command := range []string{"apply", "ping"} {
		t.Run(command, func(t *testing.T) {
			d, s, n := fixture(t)
			artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n)
			var logs bytes.Buffer
			s.Diagnostics = &logs
			d.Tool = filepath.Join(t.TempDir(), "ansible")
			os.WriteFile(d.Tool, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then printf '[core 2.20.1]'; else printf 'host unreachable\\n' >&2; exit 4; fi\n"), 0700)
			args := runmill.Arguments{}
			if command == "apply" {
				args["playbook"] = "site"
			}
			if _, err := d.Execute(t.Context(), s, command, args, nil); err == nil || !bytes.Contains(logs.Bytes(), []byte("host unreachable")) {
				t.Fatal("lost tool failure or diagnostics", err)
			}
		})
	}
}

func fixture(t *testing.T) (Provider, runmill.ExecutionContext, artifacts.Nodes) {
	t.Helper()
	shared := t.TempDir()
	work := filepath.Join(shared, "ansible")
	os.Mkdir(work, 0700)
	artifacts.Write(work, "kit-provider.json", Settings{1, "public_ipv4", "root"})
	artifacts.WriteBytes(work, "site.yml", []byte("[]"))
	s := runmill.ExecutionContext{WorkspaceDir: work, ArtifactDir: t.TempDir(), ExecutionID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Settings: runmill.Settings{"id": "ansible", "settings_file": "kit-provider.json", "producer_id": "tofu", "producer_version": "0.4.0", "artifact": "tofu-nodes-v1.json", "connection": "local", "host_key_policy": "strict", "roles": []string{"local"}, "playbooks": map[string]string{"site": "site.yml"}}}
	nodes := artifacts.Nodes{Schema: 1, Valid: true, ArtifactScope: artifacts.Scope(s.ArtifactDir), ProducerID: "tofu", ProducerVersion: "0.4.0", ExecutionID: s.ExecutionID, Nodes: []artifacts.Node{{Name: "local", Roles: []string{"local"}, Architecture: "arm64", PublicIPv4: "127.0.0.1"}}}
	return Provider{"0.4.0", "2.20.1", "/missing"}, s, nodes
}
func TestPrerequisitesRequiresOnlyCommandInputs(t *testing.T) {
	d, s, n := fixture(t)
	if _, e := d.loadExecution(t.Context(), s, "syntax-check", map[string]any{"playbook": "site"}); e != nil {
		t.Fatal(e)
	}
	if _, e := d.Execute(t.Context(), s, "apply", map[string]any{"playbook": "site"}, nil); e == nil {
		t.Fatal("missing nodes accepted")
	}
	artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n)
	if _, e := d.loadExecution(t.Context(), s, "inventory", nil); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(s.WorkspaceDir, "kit-inventory.json")); !os.IsNotExist(e) {
		t.Fatal("input validation wrote inventory")
	}
	n.ArtifactScope = "other"
	artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n)
	if _, e := d.Execute(t.Context(), s, "inventory", nil, nil); e == nil {
		t.Fatal("wrong binding accepted")
	}
}
func TestExecutionVerifiesRecap(t *testing.T) {
	for _, tc := range []struct {
		recap string
		ok    bool
	}{{`{"schema":1,"hosts":{"local":{"ok":1,"changed":1}}}`, true}, {`{"schema":1,"hosts":{"local":{"ok":1,"failures":1}}}`, false}, {`{"schema":1,"hosts":{"local":{"ok":1,"ignored":1}}}`, false}, {`{"schema":1,"hosts":{"local":{"ok":1,"rescued":1}}}`, false}, {`{"schema":1,"hosts":{"local":{"ok":1,"unreachable":1}}}`, false}, {`{"schema":1,"hosts":{"wrong":{"ok":1}}}`, false}, {`{"schema":1,"hosts":{}}`, false}, {`missing`, false}} {
		t.Run(tc.recap, func(t *testing.T) {
			d, s, n := fixture(t)
			artifacts.Write(s.ArtifactDir, "tofu-nodes-v1.json", n)
			d.Tool = filepath.Join(t.TempDir(), "ansible")
			os.WriteFile(d.Tool, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then printf '[core 2.20.1]'; else printf '%s' '"+tc.recap+"'; fi\n"), 0700)
			_, e := d.Execute(t.Context(), s, "apply", map[string]any{"playbook": "site"}, nil)
			if (e == nil) != tc.ok {
				t.Fatal(e)
			}
		})
	}
}

func TestUsesEngineSuppliedProviderDirectory(t *testing.T) {
	d, s, _ := fixture(t)
	s.Settings["id"] = "custom"

	expected := filepath.Join(filepath.Dir(s.WorkspaceDir), "custom")
	if err := os.Rename(s.WorkspaceDir, expected); err != nil {
		t.Fatal(err)
	}
	s.WorkspaceDir = expected
	// The provider must not append its own name to the engine-supplied root.
	nested := filepath.Join(expected, "ansible")
	os.Mkdir(nested, 0700)
	os.WriteFile(filepath.Join(nested, "kit-provider.json"), []byte("wrong nested root"), 0600)
	command, err := d.loadExecution(t.Context(), s, "syntax-check", map[string]any{"playbook": "site"})
	if err != nil {
		t.Fatal(err)
	}
	scope := command.scope
	if scope.WorkspaceDir != expected || scope.ArtifactDir != s.ArtifactDir {
		t.Fatal("provider changed engine-supplied workspace or artifact paths")
	}
}
