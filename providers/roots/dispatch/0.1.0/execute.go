package dispatch

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/inframill/kit/providers/roots/internal/artifacts"
	"github.com/inframill/kit/providers/roots/internal/toolrun"
)

//go:embed callback.py
var callback []byte

type Stats struct {
	OK          int `json:"ok"`
	Changed     int `json:"changed"`
	Failures    int `json:"failures"`
	Unreachable int `json:"unreachable"`
	Skipped     int `json:"skipped"`
	Rescued     int `json:"rescued"`
	Ignored     int `json:"ignored"`
}

type TargetResult struct {
	Target   string `json:"target"`
	ExitCode int    `json:"exit_code"`
}

func (r *TargetResult) UnmarshalJSON(data []byte) error {
	var wire struct {
		Target   string `json:"target"`
		ExitCode *int   `json:"exit_code"`
	}
	if err := artifacts.Decode(data, &wire); err != nil {
		return err
	}
	if wire.ExitCode == nil || wire.Target == "" {
		return errors.New("Make result requires a target and exit code")
	}
	r.Target, r.ExitCode = wire.Target, *wire.ExitCode
	return nil
}

type Recap struct {
	Schema  int                       `json:"schema"`
	Hosts   map[string]Stats          `json:"hosts"`
	Targets map[string][]TargetResult `json:"targets"`
}

type Result struct {
	Directory       string                    `json:"directory"`
	Destination     string                    `json:"destination"`
	Pushed          bool                      `json:"pushed"`
	ArchiveSHA256   string                    `json:"archive_sha256,omitempty"`
	Hosts           map[string]Stats          `json:"hosts"`
	Targets         map[string][]TargetResult `json:"targets"`
	ProviderVersion string                    `json:"provider_version"`
}

// Values are decoded by a single trusted expression. Directory paths and Make
// variable values containing Jinja syntax remain literal data, never templates.
func literal(value any) string {
	data, _ := json.Marshal(value)
	return "{{ '" + base64.StdEncoding.EncodeToString(data) + "' | b64decode | from_json }}"
}

func (p *execution) playbook(archive string) []map[string]any {
	directory := p.settings.Directories[p.args.Directory]
	tasks := []map[string]any{}
	if p.command == "push" {
		tasks = append(tasks,
			map[string]any{"name": "Create destination", "ansible.builtin.file": map[string]any{"path": literal(directory.Destination), "state": "directory", "mode": "0700"}},
			map[string]any{"name": "Create transfer staging directory", "ansible.builtin.tempfile": map[string]any{"state": "directory", "suffix": ".dispatch"}, "register": "dispatch_stage"},
			map[string]any{"name": "Push directory", "block": []map[string]any{
				{"name": "Transfer archive", "ansible.builtin.copy": map[string]any{"src": literal(archive), "dest": "{{ dispatch_stage.path }}/source.tar.gz", "mode": "0600", "decrypt": false}},
				{"name": "Extract directory", "ansible.builtin.command": map[string]any{"argv": []string{"tar", "-xzf", "{{ dispatch_stage.path }}/source.tar.gz", "-C", literal(directory.Destination), "--no-same-owner"}, "expand_argument_vars": false}},
			}, "always": []map[string]any{
				{"name": "Remove transfer staging directory", "ansible.builtin.file": map[string]any{"path": "{{ dispatch_stage.path }}", "state": "absent"}},
			}},
		)
	}
	if p.command == "push" {
		tasks = append(tasks, p.secretTasks(filepath.Dir(archive))...)
	}
	if p.command == "make" {
		argv := []string{"make", "--", p.args.Target}
		tasks = append(tasks, map[string]any{
			"name": "Run Make target", "vars": map[string]any{"dispatch_target": p.args.Target},
			"ansible.builtin.command": map[string]any{"argv": literal(argv), "chdir": literal(directory.Destination), "expand_argument_vars": false},
		})
	}
	return []map[string]any{{"name": "Dispatch workspace", "hosts": "all", "gather_facts": false, "become": directory.Become, "tasks": tasks}}
}

// Only references and temporary paths enter the playbook. Copy suppresses both
// result details and diffs; secret bytes never enter the source archive or argv.
func (p *execution) secretTasks(tmp string) []map[string]any {
	directory := p.settings.Directories[p.args.Directory]
	keys := make([]string, 0, len(directory.SecretFiles))
	for name := range directory.SecretFiles {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	parents := map[string]bool{}
	tasks := []map[string]any{}
	for index, name := range keys {
		parent := path.Dir(name)
		if parent != "." && !parents[parent] {
			tasks = append(tasks, map[string]any{"name": "Create secret directory", "ansible.builtin.file": map[string]any{
				"path": literal(path.Join(directory.Destination, parent)), "state": "directory", "mode": "0700",
			}})
			parents[parent] = true
		}
		tasks = append(tasks, map[string]any{"name": "Inject secret file", "no_log": true, "diff": false,
			"ansible.builtin.copy": map[string]any{
				"src":  literal(filepath.Join(tmp, "secret-"+strconv.Itoa(index))),
				"dest": literal(path.Join(directory.Destination, name)), "mode": "0600", "decrypt": false,
			},
		})
	}
	return tasks
}

func (p *execution) run(ctx context.Context, secrets map[string]string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inventory, selected, err := p.inventory()
	if err != nil {
		return nil, err
	}
	if err = toolrun.Public(inventory, secrets); err != nil {
		return nil, err
	}
	directory := p.settings.Directories[p.args.Directory]
	keys := make([]string, 0, len(directory.SecretFiles))
	if p.command == "push" {
		for name, reference := range directory.SecretFiles {
			value, exists := secrets[reference]
			if !exists || strings.TrimSpace(value) == "" || len(value) > artifacts.SettingsLimit {
				return nil, errors.New("missing, empty or oversized namespace secret for Dispatch file")
			}
			keys = append(keys, name)
		}
		sort.Strings(keys)
	}
	tmp, err := os.MkdirTemp(p.scope.WorkspaceDir, ".kit-dispatch-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	for index, name := range keys {
		if err = artifacts.WriteBytes(tmp, "secret-"+strconv.Itoa(index), []byte(secrets[directory.SecretFiles[name]])); err != nil {
			return nil, errors.New("could not stage Dispatch secret file")
		}
	}
	archive := filepath.Join(tmp, "source.tar.gz")
	digest := ""
	if p.command == "push" {
		file, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		err = archiveSource(ctx, p.scope.WorkspaceDir, directory, io.MultiWriter(file, hash))
		if err = errors.Join(err, file.Close()); err != nil {
			return nil, err
		}
		digest = hex.EncodeToString(hash.Sum(nil))
	}
	for name, data := range map[string][]byte{"callback.py": callback, "ansible.cfg": []byte("[defaults]\n")} {
		if err = artifacts.WriteBytes(tmp, name, data); err != nil {
			return nil, err
		}
	}
	for name, value := range map[string]any{"inventory.json": inventory, "playbook.json": p.playbook(archive)} {
		if err = artifacts.Write(tmp, name, value); err != nil {
			return nil, err
		}
	}
	tool := p.provider.Tool
	if tool == "" {
		tool = "/opt/inframill/tools/ansible/" + p.provider.ToolVersion + "/bin/ansible-playbook"
	}
	policy := "yes"
	if p.static.HostKeyPolicy == "accept-new" {
		policy = "accept-new"
	}
	env := append(toolrun.Environment(), "ANSIBLE_CONFIG="+filepath.Join(tmp, "ansible.cfg"), "ANSIBLE_CALLBACK_PLUGINS="+tmp,
		"ANSIBLE_STDOUT_CALLBACK=callback", "ANSIBLE_HOST_KEY_CHECKING=True", "ANSIBLE_RETRY_FILES_ENABLED=False",
		"ANSIBLE_SSH_ARGS=-o BatchMode=yes -o StrictHostKeyChecking="+policy)
	if p.static.Connection == "local" {
		env = append(env, "ANSIBLE_PYTHON_INTERPRETER="+filepath.Join(filepath.Dir(tool), "python3"))
	}
	run := toolrun.Runner{Tool: tool, Directory: p.scope.WorkspaceDir, Environment: env, Logs: p.scope.Diagnostics, Secrets: secrets}
	if err = run.Verify(ctx, "ansible", p.provider.ToolVersion); err != nil {
		return nil, err
	}
	if err = p.scope.Message("Dispatch " + p.command); err != nil {
		return nil, err
	}
	data, code, err := run.Call(ctx, true, "-i", filepath.Join(tmp, "inventory.json"), filepath.Join(tmp, "playbook.json"))
	if err = toolrun.Success(code, err); err != nil {
		return nil, err
	}
	var recap Recap
	if err = artifacts.Decode(data, &recap); err != nil {
		return nil, errors.New("missing or malformed Dispatch recap")
	}
	if recap.Schema != 1 || len(recap.Hosts) != len(selected) {
		return nil, errors.New("incomplete Dispatch host recap")
	}
	expectedTargets := 0
	if p.command == "make" {
		expectedTargets = 1
	}
	expectedOK := expectedTargets
	if p.command == "push" {
		expectedOK += 5 // Destination, staging, transfer, extraction and cleanup.
		expectedOK += len(p.secretTasks(tmp))
	}
	for name := range recap.Targets {
		if !slices.Contains(selected, name) {
			return nil, errors.New("unexpected host in Make results")
		}
	}
	for _, name := range selected {
		stats, ok := recap.Hosts[name]
		if !ok || stats.OK != expectedOK || stats.Changed < 0 || stats.Changed > stats.OK || stats.Skipped != 0 || stats.Failures != 0 || stats.Unreachable != 0 || stats.Rescued != 0 || stats.Ignored != 0 {
			return nil, errors.New("Dispatch did not verify every selected host")
		}
		if len(recap.Targets[name]) != expectedTargets {
			return nil, errors.New("incomplete Make results")
		}
		for _, result := range recap.Targets[name] {
			if result.Target != p.args.Target || result.ExitCode != 0 {
				return nil, errors.New("Make target did not succeed")
			}
		}
	}
	result := Result{p.args.Directory, directory.Destination, p.command == "push", digest, recap.Hosts, recap.Targets, p.provider.Version}
	if err = toolrun.Public(result, secrets); err != nil {
		return nil, err
	}
	return result, nil
}
