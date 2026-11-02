package dispatch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/inframill/kit/internal/artifacts"
	"github.com/inframill/kit/internal/toolrun"
)

type TargetResult struct {
	Target   string `json:"target"`
	ExitCode int    `json:"exit_code"`
}

type HostResult struct {
	ExitCode int `json:"exit_code"`
}

type Result struct {
	Directory       string                    `json:"directory"`
	Destination     string                    `json:"destination"`
	Pushed          bool                      `json:"pushed"`
	Hosts           map[string]HostResult     `json:"hosts"`
	Targets         map[string][]TargetResult `json:"targets"`
	ProviderVersion string                    `json:"provider_version"`
}

type BatchResult struct {
	Pushed          bool              `json:"pushed"`
	Directories     map[string]Result `json:"directories"`
	ProviderVersion string            `json:"provider_version"`
}

// Quote remote shell arguments once; payload paths and Make targets stay literal.
func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func command(argv ...string) string {
	quoted := make([]string, len(argv))
	for i, value := range argv {
		quoted[i] = quote(value)
	}
	return strings.Join(quoted, " ")
}

// One shared encoder keeps concurrent hosts' diagnostics from interleaving.
type diagnostics struct {
	mu sync.Mutex
	w  io.Writer
}

func (d *diagnostics) event(value any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return json.NewEncoder(d.w).Encode(value)
}

type hostLog struct {
	logs                    *diagnostics
	directory, host, target string
}

func (w hostLog) Write(data []byte) (int, error) {
	err := w.logs.event(map[string]any{"dispatch": "output", "directory": w.directory, "host": w.host, "target": w.target, "output": string(data)})
	return len(data), err
}

type connection struct {
	local   bool
	address string
	options []string
	runner  toolrun.Runner
}

func (c connection) run(ctx context.Context, script string, input io.Reader, capture, become bool) ([]byte, int, error) {
	runner := c.runner
	runner.Input = input
	if become {
		script = command("sudo", "-n", "--", "sh", "-c", script)
	}
	if c.local {
		runner.Tool = "/bin/sh"
		return runner.Call(ctx, capture, "-c", script)
	}
	args := append(slices.Clone(c.options), "-T", "--", c.address, script)
	return runner.Call(ctx, capture, args...)
}

func (c connection) copy(ctx context.Context, source, stage string) error {
	runner := c.runner
	var args []string
	if c.local {
		runner.Tool = "/bin/cp"
		args = []string{"-pR", "--", source, stage + "/"}
	} else {
		runner.Tool = filepath.Join(filepath.Dir(runner.Tool), "scp")
		args = append([]string{"-B", "-q", "-p", "-r", "-S", c.runner.Tool}, c.options...)
		args = append(args, "--", source, c.address+":"+stage+"/")
	}
	_, code, err := runner.Call(ctx, false, args...)
	return toolrun.Success(code, err)
}

var stagingDirectory = regexp.MustCompile(`^/tmp/inframill-dispatch\.[A-Za-z0-9]{10}$`)

func (p *execution) push(ctx context.Context, c connection, keys []string, secrets map[string]string) error {
	// SCP writes as the SSH user; only installation into destination needs sudo.
	output, code, err := c.run(ctx, "mktemp -d /tmp/inframill-dispatch.XXXXXXXXXX", nil, true, false)
	if err = toolrun.Success(code, err); err != nil {
		return err
	}
	stage := strings.TrimSpace(string(output))
	if !stagingDirectory.MatchString(stage) {
		return errors.New("invalid remote staging directory")
	}
	cleanup := "rm -rf -- " + quote(stage)
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_, _, _ = c.run(cleanupCtx, cleanup, nil, false, false)
		}
	}()
	directory := p.settings.Directories[p.args.Directory[0]]
	source := filepath.Join(p.scope.WorkspaceDir, directory.Source)
	if err = c.copy(ctx, source, stage); err != nil {
		return err
	}
	script := "set -eu\numask 077\ntrap " + quote(cleanup) + " EXIT\n" +
		command("mkdir", "-p", "--", directory.Destination) + "\n" +
		command("cp", "-pR", "--", path.Join(stage, filepath.Base(source))+"/.", directory.Destination+"/") + "\n" +
		command("chmod", "0700", directory.Destination) + "\n"
	var input []io.Reader
	for _, name := range keys {
		value := secrets[directory.SecretFiles[name]]
		input = append(input, strings.NewReader(base64.StdEncoding.EncodeToString([]byte(value))+"\n"))
		destination := path.Join(directory.Destination, name)
		script += command("install", "-d", "-m", "0700", "--", path.Dir(destination)) + "\n" +
			"IFS= read -r credential\nprintf '%s' \"$credential\" | base64 -d > " + quote(path.Join(stage, "credential")) + "\n" +
			command("install", "-m", "0600", "--", path.Join(stage, "credential"), destination) + "\n"
	}
	_, code, err = c.run(ctx, script, io.MultiReader(input...), false, directory.Become)
	err = toolrun.Success(code, err)
	return err
}

func (p *execution) run(ctx context.Context, secrets map[string]string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := toolrun.Public(p.settings, secrets); err != nil {
		return nil, err
	}
	if err := toolrun.Public(p.args, secrets); err != nil {
		return nil, err
	}
	type job struct {
		directory, host, address string
		keys                     []string
	}
	var jobs []job
	results := map[string]Result{}
	// Validate every selected mapping and its credentials before any host changes.
	for _, name := range p.args.Directory {
		selected := *p
		selected.args.Directory = DirectorySelection{name}
		hosts, err := selected.hosts() // Recheck the artifact immediately before effects.
		if err != nil {
			return nil, err
		}
		if err = toolrun.Public(hosts, secrets); err != nil {
			return nil, err
		}
		directory := p.settings.Directories[name]
		var keys []string
		if p.command == "push" {
			for file, reference := range directory.SecretFiles {
				value, exists := secrets[reference]
				if !exists || strings.TrimSpace(value) == "" || len(value) > artifacts.SettingsLimit {
					return nil, errors.New("missing, empty or oversized namespace secret for Dispatch file")
				}
				keys = append(keys, file)
			}
			sort.Strings(keys)
		}
		results[name] = Result{Directory: name, Destination: directory.Destination, Pushed: p.command == "push",
			Hosts: map[string]HostResult{}, Targets: map[string][]TargetResult{}, ProviderVersion: p.provider.Version}
		for host, address := range hosts {
			jobs = append(jobs, job{directory: name, host: host, address: address, keys: keys})
		}
	}
	tool := p.provider.Tool
	var err error
	if p.static.Connection == "ssh" && tool == "" {
		if tool, err = exec.LookPath("ssh"); err != nil {
			return nil, err
		}
	}
	policy := "yes"
	if p.static.HostKeyPolicy == "accept-new" {
		policy = "accept-new"
	}
	options := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=" + policy, "-o", "ConnectTimeout=15"}
	if p.static.Connection == "ssh" {
		knownHosts, err := filepath.Abs(filepath.Join(p.scope.ArtifactDir, p.static.KnownHostsProviderID+"-known_hosts"))
		if err != nil {
			return nil, err
		}
		// SSH parses a file list inside this argument; preserve spaces and quotes.
		options = append(options, "-o", "UserKnownHostsFile="+strconv.Quote(knownHosts))
	}
	sink := p.scope.Diagnostics
	if sink == nil {
		sink = io.Discard
	}
	logs := &diagnostics{w: sink}
	if err = p.scope.Message("Dispatch " + p.command); err != nil {
		return nil, err
	}
	var mu sync.Mutex
	var failures []error
	var workers sync.WaitGroup
	parallel := make(chan struct{}, 8)
	previousDirectory := ""
hostsLoop:
	for _, task := range jobs {
		if p.command == "make" && task.directory != previousDirectory {
			workers.Wait()
			if len(failures) > 0 {
				break
			}
		}
		previousDirectory = task.directory
		select {
		case parallel <- struct{}{}:
		case <-ctx.Done():
			break hostsLoop
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-parallel }()
			selected := *p
			selected.args.Directory = DirectorySelection{task.directory}
			directory := p.settings.Directories[task.directory]
			c := connection{local: p.static.Connection == "local", address: p.settings.SSHUser + "@" + task.address,
				options: options,
				runner: toolrun.Runner{Tool: tool, Directory: p.scope.WorkspaceDir, Environment: toolrun.Environment(),
					Logs: hostLog{logs: logs, directory: task.directory, host: task.host, target: p.args.Target}, Secrets: secrets}}
			var hostErr error
			code := 0
			if p.command == "push" {
				hostErr = selected.push(ctx, c, task.keys, secrets)
			} else {
				script := "cd -- " + quote(directory.Destination) + " && exec " + command("make", "--", p.args.Target)
				_, code, hostErr = c.run(ctx, script, nil, false, directory.Become)
				hostErr = errors.Join(toolrun.Success(code, hostErr), logs.event(map[string]any{
					"dispatch": "make", "directory": task.directory, "host": task.host, "target": p.args.Target, "exit_code": code}))
			}
			mu.Lock()
			defer mu.Unlock()
			if hostErr != nil {
				failures = append(failures, fmt.Errorf("%s/%s: %w", task.directory, task.host, hostErr))
				return
			}
			results[task.directory].Hosts[task.host] = HostResult{ExitCode: 0}
			if p.command == "make" {
				results[task.directory].Targets[task.host] = []TargetResult{{Target: p.args.Target, ExitCode: code}}
			}
		}()
	}
	workers.Wait()
	if err = errors.Join(append(failures, ctx.Err())...); err != nil {
		return nil, err
	}
	var result any = BatchResult{Pushed: p.command == "push", Directories: results, ProviderVersion: p.provider.Version}
	if len(p.args.Directory) == 1 {
		result = results[p.args.Directory[0]]
	}
	if err = toolrun.Public(result, secrets); err != nil {
		return nil, err
	}
	return result, nil
}
