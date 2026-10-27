package dispatch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
	return Provider{"0.1.0", "2.20.1", "/missing"}, scope, settings, nodes
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
		inventory, selected, err := p.inventory()
		if err != nil || !reflect.DeepEqual(selected, tc.want) || len(inventory.All.Hosts) != len(tc.want) {
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

func TestArchiveExcludesPermissionsAndLinks(t *testing.T) {
	_, scope, settings, _ := fixture(t)
	source := filepath.Join(scope.WorkspaceDir, "api")
	for name, content := range map[string]string{".git/config": "excluded", "nested/private.env": "excluded", "nested/run.sh": "#!/bin/sh\nexit 0\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(source, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	directory := settings.Directories["api"]
	directory.Exclude = []string{".git/**", "**/*.env"}
	var output bytes.Buffer
	if err := archiveSource(t.Context(), scope.WorkspaceDir, directory, &output); err != nil {
		t.Fatal(err)
	}
	compressed, err := gzip.NewReader(&output)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	files := map[string]int64{}
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = header.Mode
	}
	if files["nested/run.sh"] != 0700 || files["Makefile"] != 0600 || len(files) != 3 {
		t.Fatal(files)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := walkSource(t.Context(), scope.WorkspaceDir, directory, nil); err == nil {
		t.Fatal("accepted source link")
	}
	if err := os.Symlink(source, filepath.Join(scope.WorkspaceDir, "linked")); err != nil {
		t.Fatal(err)
	}
	directory.Source = "linked"
	if err := walkSource(t.Context(), scope.WorkspaceDir, directory, nil); err == nil {
		t.Fatal("accepted linked source root")
	}
	for _, patterns := range [][]string{{"../escape"}, {"nested/**bad"}, {"["}} {
		if validateExcludes(patterns) == nil {
			t.Fatalf("accepted exclude %v", patterns)
		}
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

func stubTool(t *testing.T, recap string) string {
	t.Helper()
	tool := filepath.Join(t.TempDir(), "ansible-playbook")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '[core 2.20.1]'; exit 0; fi\ncp \"$2\" captured-inventory.json\ncp \"$3\" captured-playbook.json\nprintf '%s' '" + recap + "'\n"
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return tool
}

func TestExecutionVerifiesHostsTargetsAndRechecksArtifact(t *testing.T) {
	for _, tc := range []struct {
		recap string
		ok    bool
	}{
		{`{"schema":1,"hosts":{"web-1":{"ok":1}},"targets":{"web-1":[{"target":"deploy","exit_code":0}]}}`, true},
		{`{"schema":1,"hosts":{"web-1":{"ok":1}},"targets":{"web-1":[{"target":"deploy"}]}}`, false},
		{`{"schema":1,"hosts":{"web-1":{"ok":1}},"targets":{}}`, false},
		{`{"schema":1,"hosts":{"web-1":{"ok":1}},"targets":{"web-1":[{"target":"other","exit_code":0}]}}`, false},
		{`{"schema":1,"hosts":{"web-1":{"ok":1}},"targets":{"web-1":[{"target":"deploy","exit_code":2}]}}`, false},
		{`{"schema":1,"hosts":{"web-1":{"ok":1,"ignored":1}},"targets":{"web-1":[{"target":"deploy","exit_code":0}]}}`, false},
		{`{"schema":1,"hosts":{"web-1":{"ok":1,"skipped":1}},"targets":{"web-1":[{"target":"deploy","exit_code":0}]}}`, false},
		{`{"schema":1,"hosts":{"other":{"ok":1}},"targets":{}}`, false},
		{`{"schema":1,"hosts":{},"targets":{}}`, false},
		{`malformed`, false},
	} {
		t.Run(tc.recap, func(t *testing.T) {
			d, scope, settings, nodes := fixture(t)
			directory := settings.Directories["api"]
			directory.Select = Selector{Names: []string{"web-1"}}
			settings.Directories["api"] = directory
			writeFixture(t, scope, settings, nodes)
			d.Tool = stubTool(t, tc.recap)
			result, err := d.Execute(t.Context(), scope, "make", runmill.Arguments{"directory": "api", "target": "deploy"}, nil)
			if (err == nil) != tc.ok {
				t.Fatalf("result %v: %v", result, err)
			}
			entries, _ := os.ReadDir(scope.WorkspaceDir)
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".kit-dispatch-") {
					t.Fatal("temporary execution files retained")
				}
			}
		})
	}
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

func TestRealAnsiblePushAndMake(t *testing.T) {
	tool := os.Getenv("DISPATCH_ANSIBLE_PATH")
	if tool == "" {
		t.Skip("set DISPATCH_ANSIBLE_PATH to exercise real local Ansible and Make")
	}
	d, scope, settings, nodes := fixture(t)
	d.Tool = tool
	var logs bytes.Buffer
	scope.Diagnostics = &logs
	directory := settings.Directories["api"]
	directory.Select = Selector{Names: []string{"web-1"}}
	directory.Exclude = []string{"**/*.env"}
	directory.SecretFiles = map[string]string{"secrets/root_token": "token", "tls/private-key.pem": "tls-key"}
	settings.Directories["api"] = directory
	writeFixture(t, scope, settings, nodes)
	if err := os.MkdirAll(directory.Destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory.Destination, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	makefile := ".PHONY: build deploy fail\nbuild:\n\t@printf '%s' '{{ literal_value }}' > built.txt\n\t@echo synthetic-token\ndeploy:\n\t@test -f built.txt\n\t@echo deployed\nfail:\n\t@echo failure-detail >&2\n\t@exit 7\n"
	for name, contents := range map[string]string{"Makefile": makefile, "private.env": "excluded", "run.sh": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(scope.WorkspaceDir, "api", name), []byte(contents), 0700); err != nil {
			t.Fatal(err)
		}
	}
	args := runmill.Arguments{"directory": "api"}
	p, err := d.loadExecution(t.Context(), scope, "push", args)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := "synthetic-private-key\n{{ lookup('pipe', 'false') }}\n$literal\n"
	value, err := p.run(t.Context(), map[string]string{"token": "synthetic-token", "tls-key": privateKey})
	if err != nil {
		t.Fatalf("%v\n%s", err, logs.String())
	}
	result := value.(Result)
	if !result.Pushed || len(result.ArchiveSHA256) != 64 || len(result.Targets["web-1"]) != 0 {
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
	// A repeated push refreshes secret files even when the payload is unchanged.
	if _, err = p.run(t.Context(), map[string]string{"token": "rotated-token", "tls-key": privateKey, "previous-token": "synthetic-token"}); err != nil {
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
	if _, err = os.Stat(filepath.Join(directory.Destination, "private.env")); !os.IsNotExist(err) {
		t.Fatal("excluded file transferred", err)
	}
	info, err := os.Stat(filepath.Join(directory.Destination, "run.sh"))
	if err != nil || info.Mode().Perm()&0100 == 0 {
		t.Fatal("executable permission lost", err)
	}
	if _, err = os.Stat(filepath.Join(directory.Destination, "keep.txt")); err != nil {
		t.Fatal("extra destination file removed", err)
	}
	if strings.Contains(logs.String(), "synthetic-token") || !strings.Contains(logs.String(), "[REDACTED]") || !strings.Contains(logs.String(), "deployed") {
		t.Fatal(logs.String())
	}
	for _, target := range []string{"deploy", "fail"} {
		logs.Reset()
		value, err := d.Execute(t.Context(), scope, "make", runmill.Arguments{"directory": "api", "target": target}, nil)
		if target == "deploy" && (err != nil || value.(Result).Pushed) {
			t.Fatalf("%v: %v\n%s", value, err, logs.String())
		}
		if target == "fail" && (err == nil || !strings.Contains(logs.String(), "failure-detail")) {
			t.Fatalf("%v\n%s", err, logs.String())
		}
	}
}

func TestDefinitionAdvertisesIndependentCommands(t *testing.T) {
	definition, err := (Provider{}).Definition()
	if err != nil || definition.Name != "dispatch" || len(definition.Commands) != 2 {
		t.Fatal(definition, err)
	}
	for _, command := range definition.Commands {
		if command.AccessMode != runmill.ReadWrite {
			t.Fatal("remote effects require exclusive Instance access")
		}
		if command.Description == "" {
			t.Fatal("missing command description")
		}
	}
}
