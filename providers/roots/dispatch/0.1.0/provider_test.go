package dispatch

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/inframill/kit/providers/roots/internal/artifacts"
	"github.com/sir-hassan/inframill/pkg/runmill"
)

func fixture(t *testing.T) (Provider, runmill.ExecutionContext, Settings, artifacts.Nodes) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "custom-dispatch")
	if err := os.MkdirAll(filepath.Join(work, "api"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "api", "Makefile"), []byte("deploy:\n\t@echo deployed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	scope := runmill.ExecutionContext{WorkspaceDir: work, ArtifactDir: t.TempDir(), ExecutionID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Settings: runmill.Settings{
		"id": "custom-dispatch", "settings_file": "kit-provider.json", "producer_id": "opentofu", "producer_version": "0.4.0", "artifact": "opentofu-nodes-v1.json", "connection": "local",
	}}
	settings := Settings{Schema: 1, AddressMode: "public_ipv4", SSHUser: "root", Directories: map[string]Directory{
		"api": {Source: "api", Destination: filepath.Join(t.TempDir(), "remote {{ untouched }} space"), Select: Selector{Roles: []string{"web"}}},
	}}
	nodes := artifacts.Nodes{Schema: 1, Valid: true, ArtifactScope: artifacts.Scope(scope.ArtifactDir), ProducerID: "opentofu", ProducerVersion: "0.4.0", ExecutionID: scope.ExecutionID, Nodes: []artifacts.Node{
		{Name: "web-1", Roles: []string{"common", "web"}, Architecture: "arm64", PublicIPv4: "127.0.0.1"},
		{Name: "web-2", Roles: []string{"web"}, Architecture: "amd64", PublicIPv4: "127.0.0.2"},
		{Name: "db-1", Roles: []string{"data"}, Architecture: "arm64"}, // Unselected hosts need no address.
	}}
	writeFixture(t, scope, settings, nodes)
	return Provider{Version: "0.1.0"}, scope, settings, nodes
}

func writeFixture(t *testing.T, scope runmill.ExecutionContext, settings Settings, nodes artifacts.Nodes) {
	t.Helper()
	for _, err := range []error{artifacts.Write(scope.WorkspaceDir, "kit-provider.json", settings), artifacts.Write(scope.ArtifactDir, "opentofu-nodes-v1.json", nodes)} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSelectorsAndArtifactBinding(t *testing.T) {
	d, scope, settings, nodes := fixture(t)
	for _, tc := range []struct {
		selectNodes Selector
		want        []string
	}{
		{Selector{Roles: []string{"web"}}, []string{"web-1", "web-2"}},
		{Selector{Roles: []string{"web"}, Architecture: "arm64"}, []string{"web-1"}},
		{Selector{Names: []string{"web-2"}}, []string{"web-2"}},
		{Selector{Roles: []string{"common", "web"}, Architecture: "arm64"}, []string{"web-1"}},
	} {
		directory := settings.Directories["api"]
		directory.Select = tc.selectNodes
		settings.Directories["api"] = directory
		writeFixture(t, scope, settings, nodes)
		args := runmill.Arguments{"directory": "api"}
		p, err := d.loadExecution(t.Context(), scope, "push", args)
		if err != nil {
			t.Fatal(err)
		}
		hosts, err := p.hosts()
		var selected []string
		for name := range hosts {
			selected = append(selected, name)
		}
		sort.Strings(selected)
		if err != nil || !reflect.DeepEqual(selected, tc.want) {
			t.Fatalf("selector %v: %v %v", tc.selectNodes, selected, err)
		}
	}
	for _, selector := range []Selector{{Names: []string{"db-1"}}, {Names: []string{"missing"}}, {Roles: []string{"missing"}}, {Names: []string{"web-1", "web-1"}}, {Architecture: "other"}} {
		directory := settings.Directories["api"]
		directory.Select = selector
		settings.Directories["api"] = directory
		writeFixture(t, scope, settings, nodes)
		if _, err := d.Execute(t.Context(), scope, "push", runmill.Arguments{"directory": "api"}, nil); err == nil {
			t.Fatalf("accepted selector %v", selector)
		}
	}
	directory := settings.Directories["api"]
	directory.Select = Selector{Roles: []string{"web"}}
	settings.Directories["api"] = directory
	for _, mutation := range []func(*artifacts.Nodes){
		func(n *artifacts.Nodes) { n.Valid = false },
		func(n *artifacts.Nodes) { n.ArtifactScope = "other" },
		func(n *artifacts.Nodes) { n.ProducerVersion = "other" },
		func(n *artifacts.Nodes) { n.Nodes[1].PublicIPv4 = n.Nodes[0].PublicIPv4 },
	} {
		var changed artifacts.Nodes
		if err := decode(nodes, &changed); err != nil {
			t.Fatal(err)
		}
		mutation(&changed)
		writeFixture(t, scope, settings, changed)
		if _, err := d.Execute(t.Context(), scope, "push", runmill.Arguments{"directory": "api"}, nil); err == nil {
			t.Fatal("accepted invalid node artifact")
		}
	}
}

func TestPrerequisitesValidatesInputsWithoutEffects(t *testing.T) {
	d, scope, settings, nodes := fixture(t)
	before, _ := artifacts.SourceHash(scope.WorkspaceDir)
	p, err := d.loadExecution(t.Context(), scope, "push", runmill.Arguments{"directory": "api"})
	if err != nil || p.scope.WorkspaceDir != scope.WorkspaceDir {
		t.Fatal(err)
	}
	after, _ := artifacts.SourceHash(scope.WorkspaceDir)
	if before != after {
		t.Fatal("input validation changed workspace")
	}
	for _, args := range []runmill.Arguments{
		nil, {"directory": "missing"}, {"directory": "api", "unknown": true},
		{"directory": []string{}}, {"directory": []string{"api", "api"}},
		{"directory": []string{"api", "missing"}}, {"directory": []any{"api", nil}},
		{"directory": true},
		{"directory": "api", "targets": []string{"-f"}}, {"directory": "api", "targets": []string{"deploy;id"}},
		{"directory": "api", "targets": []string{"deploy"}},
		{"directory": "api", "target": "deploy"}, {"directory": "api", "target": ""},
		{"directory": "api", "target": nil}, {"directory": "api", "select": Selector{}},
		{"directory": "api", "variables": map[string]string{"X": "value"}},
		{"directory": "api", "targets": []string{"deploy"}, "variables": map[string]string{"-f": "value"}},
	} {
		if _, err := d.Execute(t.Context(), scope, "push", args, nil); err == nil {
			t.Fatalf("accepted arguments %v", args)
		}
	}
	if _, err := d.Execute(t.Context(), scope, "make", runmill.Arguments{"directory": "api"}, nil); err == nil {
		t.Fatal("make accepted no target")
	}
	for _, args := range []runmill.Arguments{
		{"directory": "api", "target": ""}, {"directory": "api", "target": nil},
		{"directory": "api", "target": 42}, {"directory": "api", "target": []string{"deploy"}},
		{"directory": "api", "target": "-f"}, {"directory": "api", "target": "deploy;id"},
		{"directory": "api", "target": "build deploy"},
		{"directory": "api", "targets": []string{"deploy"}},
		{"directory": "api", "target": "deploy", "select": Selector{}},
		{"directory": "api", "target": "deploy", "variables": map[string]string{"X": "value"}},
	} {
		if _, err := d.Execute(t.Context(), scope, "make", args, nil); err == nil {
			t.Fatalf("make accepted arguments %v", args)
		}
	}
	for _, source := range []string{".", "../other", "/tmp", "api/../api"} {
		directory := settings.Directories["api"]
		directory.Source = source
		settings.Directories["api"] = directory
		writeFixture(t, scope, settings, nodes)
		if _, err := d.Execute(t.Context(), scope, "push", runmill.Arguments{"directory": "api"}, nil); err == nil {
			t.Fatalf("accepted source %q", source)
		}
	}
	settings.Directories["api"] = Directory{Source: "absent", Destination: "/opt/api", Select: Selector{Roles: []string{"web"}}}
	writeFixture(t, scope, settings, nodes)
	// Make operates on the existing remote directory, without reading source files.
	if _, err := d.loadExecution(t.Context(), scope, "make", runmill.Arguments{"directory": "api", "target": "deploy"}); err != nil {
		t.Fatal(err)
	}
}

func TestSecretFilesValidateAndRequireCredentialsBeforeEffects(t *testing.T) {
	d, scope, settings, nodes := fixture(t)
	directory := settings.Directories["api"]
	for _, files := range []map[string]string{
		{"../escape": "token"}, {"/absolute": "token"}, {".": "token"},
		{"secrets/token": ""}, {"secrets/token": "INVALID"}, {"secrets\n/token": "token"},
		{"secrets": "token", "secrets/token": "token"},
	} {
		directory.SecretFiles = files
		settings.Directories["api"] = directory
		writeFixture(t, scope, settings, nodes)
		if _, err := d.Execute(t.Context(), scope, "push", runmill.Arguments{"directory": "api"}, nil); err == nil {
			t.Fatalf("accepted invalid secret file mapping %v", files)
		}
	}
	directory.SecretFiles = map[string]string{"secrets/root_token": "token"}
	settings.Directories["api"] = directory
	writeFixture(t, scope, settings, nodes)
	before, _ := artifacts.SourceHash(scope.WorkspaceDir)
	p, err := d.loadExecution(t.Context(), scope, "push", runmill.Arguments{"directory": "api"})
	if err != nil {
		t.Fatal(err)
	}
	for _, secrets := range []map[string]string{nil, {"token": ""}, {"token": "\n "}, {"token": strings.Repeat("x", artifacts.SettingsLimit+1)}} {
		if _, err := p.run(t.Context(), secrets); err == nil {
			t.Fatal("accepted missing, empty or oversized credential")
		}
	}
	after, _ := artifacts.SourceHash(scope.WorkspaceDir)
	if before != after {
		t.Fatal("secret validation changed workspace")
	}
	if _, err = os.Stat(directory.Destination); !os.IsNotExist(err) {
		t.Fatal("secret validation created a remote destination", err)
	}
}

func TestSourceRejectsLinks(t *testing.T) {
	_, scope, _, _ := fixture(t)
	source := filepath.Join(scope.WorkspaceDir, "api")
	if err := os.Symlink(t.TempDir(), filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := validateSource(t.Context(), scope.WorkspaceDir, "api"); err == nil {
		t.Fatal("accepted source link")
	}
	if err := os.Symlink(source, filepath.Join(scope.WorkspaceDir, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := validateSource(t.Context(), scope.WorkspaceDir, "linked"); err == nil {
		t.Fatal("accepted linked source root")
	}
}

func TestMakeRejectsSecretBearingTargetBeforeDiagnostics(t *testing.T) {
	d, scope, _, _ := fixture(t)
	var logs bytes.Buffer
	scope.Diagnostics = &logs
	_, err := d.Execute(t.Context(), scope, "make", runmill.Arguments{"directory": "api", "target": "deploy"}, map[string]string{"token": "deploy"})
	if err == nil || strings.Contains(logs.String(), "deploy") {
		t.Fatal("secret-bearing target entered execution diagnostics", err)
	}
}

func TestArtifactRetirementAndCancellation(t *testing.T) {
	d, scope, _, _ := fixture(t)
	p, err := d.loadExecution(t.Context(), scope, "push", runmill.Arguments{"directory": "api"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(scope.ArtifactDir, "opentofu-nodes-v1.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = p.run(t.Context(), nil); err == nil {
		t.Fatal("execution accepted retired artifact")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = p.run(ctx, nil); err != context.Canceled {
		t.Fatal(err)
	}
}

// Exercise real copies, secret delivery and Make; the SSH variant substitutes
// only the network boundary, executing the actual remote scripts locally.
func testWorkflow(t *testing.T, useSSH bool) {
	t.Helper()
	d, scope, settings, nodes := fixture(t)
	var logs bytes.Buffer
	scope.Diagnostics = &logs
	directory := settings.Directories["api"]
	directory.Destination = filepath.Join(t.TempDir(), "remote ' {{ literal }} $(false) space")
	directory.Select = Selector{Names: []string{"web-1"}}
	directory.SecretFiles = map[string]string{"secrets/root_token": "token", "tls/private-key.pem": "tls-key"}
	settings.Directories["api"] = directory
	if useSSH {
		scope.Settings["connection"] = "ssh"
		scope.Settings["host_key_policy"] = "accept-new"
		directory.Become = true
		directory.LegacyExclude = []string{"**/*.env", ".hidden"}
		settings.Directories["api"] = directory
		tools := t.TempDir()
		d.Tool = filepath.Join(tools, "ssh")
		for name, contents := range map[string]string{
			"ssh": `#!/bin/sh
set -eu
printf '%s\n' "$@" >> ssh-arguments
while [ "$1" != -- ]; do shift; done
shift
test "$1" = root@127.0.0.1
shift
exec /bin/sh -c "$1"
`,
			"scp": `#!/bin/sh
set -eu
printf '%s\n' "$@" >> scp-arguments
while [ "$1" != -- ]; do shift; done
shift
destination=${2#*:}
exec /bin/cp -pR -- "$1" "$destination"
`,
			"sudo": "#!/bin/sh\nset -eu\ntest \"$1\" = -n\nshift\ntest \"$1\" = --\nshift\nexec \"$@\"\n",
		} {
			if err := os.WriteFile(filepath.Join(tools, name), []byte(contents), 0700); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	writeFixture(t, scope, settings, nodes)
	if err := os.MkdirAll(directory.Destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory.Destination, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	makefile := ".PHONY: build deploy fail\nbuild:\n\t@printf '%s' '{{ literal_value }}' > built.txt\n\t@echo synthetic-token\ndeploy:\n\t@test -f built.txt\n\t@echo deployed\nfail:\n\t@echo failure-detail >&2\n\t@exit 7\n"
	source := filepath.Join(scope.WorkspaceDir, "api")
	for name, contents := range map[string]string{"Makefile": makefile, "private.env": "copied", ".hidden": "copied too", "run.sh": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(contents), 0700); err != nil {
			t.Fatal(err)
		}
	}
	modified := time.Unix(1700000000, 0)
	if err := os.Chtimes(filepath.Join(source, "run.sh"), modified, modified); err != nil {
		t.Fatal(err)
	}
	privateKey := "synthetic-private-key\n{{ lookup('pipe', 'false') }}\n$literal\n\x00last"
	args := runmill.Arguments{"directory": "api"}
	value, err := d.Execute(t.Context(), scope, "push", args, map[string]string{"token": "synthetic-token", "tls-key": privateKey})
	if err != nil {
		t.Fatalf("%v\n%s", err, logs.String())
	}
	result := value.(Result)
	if !result.Pushed || len(result.Hosts) != 1 || len(result.Targets) != 0 {
		t.Fatal(result)
	}
	for name, expected := range map[string]string{"secrets/root_token": "synthetic-token", "tls/private-key.pem": privateKey} {
		data, err := os.ReadFile(filepath.Join(directory.Destination, name))
		if err != nil || string(data) != expected {
			t.Fatal("secret bytes changed in transit", err)
		}
		info, err := os.Stat(filepath.Join(directory.Destination, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("secret file permissions", err)
		}
	}
	if strings.Contains(logs.String(), "synthetic-private-key") {
		t.Fatal("secret file contents appeared in diagnostics")
	}
	if _, err = d.Execute(t.Context(), scope, "push", args, map[string]string{"token": "rotated-token", "tls-key": privateKey}); err != nil {
		t.Fatal(err)
	}
	rotated, err := os.ReadFile(filepath.Join(directory.Destination, "secrets/root_token"))
	if err != nil || string(rotated) != "rotated-token" {
		t.Fatal("secret rotation was not applied", err)
	}
	for _, target := range []string{"build", "deploy"} {
		value, err := d.Execute(t.Context(), scope, "make", runmill.Arguments{"directory": "api", "target": target}, map[string]string{"token": "synthetic-token"})
		if err != nil || value.(Result).Pushed || len(value.(Result).Targets["web-1"]) != 1 {
			t.Fatalf("%v: %v\n%s", value, err, logs.String())
		}
	}
	data, err := os.ReadFile(filepath.Join(directory.Destination, "built.txt"))
	if err != nil || string(data) != "{{ literal_value }}" {
		t.Fatalf("literal Make value: %q %v", data, err)
	}
	for _, name := range []string{"private.env", ".hidden", "keep.txt"} {
		if _, err = os.Stat(filepath.Join(directory.Destination, name)); err != nil {
			t.Fatal("missing complete payload or preserved destination file", name, err)
		}
	}
	info, err := os.Stat(filepath.Join(directory.Destination, "run.sh"))
	if err != nil || info.Mode().Perm() != 0700 || !info.ModTime().Equal(modified) {
		t.Fatal("source permissions or modification time lost", err)
	}
	if strings.Contains(logs.String(), "synthetic-token") || !strings.Contains(logs.String(), "[REDACTED]") || !strings.Contains(logs.String(), "deployed") {
		t.Fatal(logs.String())
	}
	logs.Reset()
	if _, err = d.Execute(t.Context(), scope, "make", runmill.Arguments{"directory": "api", "target": "fail"}, nil); err == nil || !strings.Contains(logs.String(), "failure-detail") {
		t.Fatalf("%v\n%s", err, logs.String())
	}
	if useSSH {
		ssh, _ := os.ReadFile(filepath.Join(scope.WorkspaceDir, "ssh-arguments"))
		scp, _ := os.ReadFile(filepath.Join(scope.WorkspaceDir, "scp-arguments"))
		if !bytes.Contains(ssh, []byte("StrictHostKeyChecking=accept-new")) || !bytes.Contains(scp, []byte("-r\n")) {
			t.Fatal("missing SSH policy or recursive SCP", string(ssh), string(scp))
		}
		if bytes.Contains(ssh, []byte("synthetic-token")) || bytes.Contains(ssh, []byte("synthetic-private-key")) {
			t.Fatal("credentials entered SSH arguments")
		}
	}
}

func TestLocalPushAndMake(t *testing.T) { testWorkflow(t, false) }
func TestSCPAndSSH(t *testing.T)        { testWorkflow(t, true) }

func TestMultipleDirectoriesPreflightPushAndOrderedMake(t *testing.T) {
	d, scope, settings, nodes := fixture(t)
	root := t.TempDir()
	order := filepath.Join(root, "order")
	apiReady := filepath.Join(root, "api-ready")
	unexpected := filepath.Join(root, "should-not-run")
	api := settings.Directories["api"]
	api.Destination = filepath.Join(root, "api")
	api.Select = Selector{Names: []string{"web-1"}}
	settings.Directories["api"] = api
	settings.Directories["worker"] = Directory{Source: "worker", Destination: filepath.Join(root, "worker"),
		Select: Selector{Names: []string{"web-2"}}, SecretFiles: map[string]string{"secrets/token": "worker-token"}}
	if err := os.Mkdir(filepath.Join(scope.WorkspaceDir, "worker"), 0700); err != nil {
		t.Fatal(err)
	}
	apiMake := "start:\n\t@touch " + quote(apiReady) + "\n\t@echo api >> " + quote(order) + "\nfail:\n\t@touch " + quote(unexpected) + "\n"
	workerMake := "start:\n\t@test -f " + quote(apiReady) + "\n\t@echo worker >> " + quote(order) + "\nfail:\n\t@exit 7\n"
	for name, contents := range map[string]string{"api": apiMake, "worker": workerMake} {
		if err := os.WriteFile(filepath.Join(scope.WorkspaceDir, name, "Makefile"), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(t, scope, settings, nodes)
	args := runmill.Arguments{"directory": []string{"api", "worker"}}
	if _, err := d.Execute(t.Context(), scope, "push", args, nil); err == nil {
		t.Fatal("batch accepted missing credentials")
	}
	if _, err := os.Stat(api.Destination); !os.IsNotExist(err) {
		t.Fatal("batch changed a host before validating all credentials", err)
	}
	value, err := d.Execute(t.Context(), scope, "push", args, map[string]string{"worker-token": "synthetic-worker-credential"})
	if err != nil {
		t.Fatal(err)
	}
	pushed := value.(BatchResult)
	if !pushed.Pushed || len(pushed.Directories) != 2 || len(pushed.Directories["api"].Hosts) != 1 || len(pushed.Directories["worker"].Hosts) != 1 {
		t.Fatal(pushed)
	}
	if _, ok := pushed.Directories["worker"].Hosts["web-2"]; !ok {
		t.Fatal("batch ignored a mapping's host selector", pushed)
	}
	credential, err := os.ReadFile(filepath.Join(settings.Directories["worker"].Destination, "secrets/token"))
	if err != nil || string(credential) != "synthetic-worker-credential" {
		t.Fatal("batch did not inject the selected mapping's secret", err)
	}
	args["target"] = "start"
	value, err = d.Execute(t.Context(), scope, "make", args, nil)
	if err != nil || value.(BatchResult).Directories["worker"].Targets["web-2"][0].Target != "start" {
		t.Fatal("batch Make failed", value, err)
	}
	started, err := os.ReadFile(order)
	if err != nil || string(started) != "api\nworker\n" {
		t.Fatal("Make did not preserve the requested directory order", err)
	}
	_, err = d.Execute(t.Context(), scope, "make", runmill.Arguments{
		"directory": []string{"worker", "api"}, "target": "fail"}, nil)
	if err == nil {
		t.Fatal("batch ignored a failed Make target")
	}
	if _, err := os.Stat(unexpected); !os.IsNotExist(err) {
		t.Fatal("Make continued to the next directory after failure", err)
	}
}

func TestDefinitionAdvertisesIndependentCommands(t *testing.T) {
	definition, err := (Provider{}).Definition()
	if err != nil || definition.Name != "dispatch" || len(definition.Commands) != 2 {
		t.Fatal(definition, err)
	}
	for _, command := range definition.Commands {
		if command.AccessMode != runmill.ReadWrite || command.Description == "" {
			t.Fatal("missing command contract")
		}
	}
}
