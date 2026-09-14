//go:build architecture

package architecture_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// SPEC-0005 REQ-001: compile only after regeneration and discard the binary.
func TestREQ001FastWorkflow(t *testing.T) {
	plan := taskPlan(t, "check-fast")
	requireTaskOrder(t, plan,
		"go tool buf generate --template connect-rpc/buf.gen.yaml",
		"go build -o /dev/null ./cmd",
		"go test -vet=off ./cmd/... ./internal/... ./pkg/inframill/... ./pkg/driver/...",
	)
	for _, excluded := range []string{"tests/cli", "tests/architecture", "tests/engine", "npm", "node", "INFRAMILL_BINARY_OUTPUT"} {
		if strings.Contains(plan, excluded) {
			t.Errorf("fast workflow unexpectedly includes %s", excluded)
		}
	}
}

// SPEC-0005 REQ-002: all remaining suites run after fast, with fresh TS bindings.
func TestREQ002FullWorkflow(t *testing.T) {
	requireTaskOrder(t, taskPlan(t, "check-full"),
		"go tool buf generate --template connect-rpc/buf.gen.yaml",
		"go build -o /dev/null ./cmd",
		"go test -vet=off ./cmd/... ./internal/... ./pkg/inframill/... ./pkg/driver/...",
		"scripts/spec-check.sh",
		"go test -vet=off -tags architecture ./tests/architecture",
		"go test -vet=off ./tests/cli/... ./tests/engine/fixture/...",
		"go tool buf generate --template connect-rpc/buf.gen.ts.yaml",
		"npm --prefix tests/engine test",
		"scripts/check-connect-generated.sh",
		"scripts/check-connect-typescript.sh",
	)
}

// SPEC-0005 REQ-003/005: no automatic entry point can indirectly invoke lint.
// Dry runs inspect command expansion; they never execute any lint tool.
func TestREQ003AutomaticTasksNeverLint(t *testing.T) {
	var config struct {
		Tasks map[string]yaml.Node `yaml:"tasks"`
	}
	readTaskYAML(t, "Taskfile.yml", &config)
	for name := range config.Tasks {
		if name == "lint" {
			continue
		}
		if (strings.Contains(name, "check") || strings.Contains(name, "test")) && name != "check-fast" && name != "check-full" {
			t.Errorf("additional check/test entry point: %s", name)
		}
		t.Run(name, func(t *testing.T) {
			plan := taskPlan(t, name)
			for _, forbidden := range []string{"golangci-lint", "go vet", "buf lint", "gofmt", "git diff --check", "bash -n"} {
				if strings.Contains(plan, forbidden) {
					t.Errorf("automatic task invokes %s", forbidden)
				}
			}
			for line := range strings.SplitSeq(plan, "\n") {
				if strings.Contains(line, "go test ") && !strings.Contains(line, "-vet=off") {
					t.Errorf("test command allows implicit vet: %s", line)
				}
			}
		})
	}
	if _, exists := config.Tasks["lint"]; !exists {
		t.Fatal("manual lint task is missing")
	}
	for _, old := range []string{"go:fix", "go:test", "test:engine", "check:spec", "check:fast", "check:handoff", "connect:check", "connect:check-ts"} {
		if _, exists := config.Tasks[old]; exists {
			t.Errorf("obsolete task remains: %s", old)
		}
	}
}

// SPEC-0005 REQ-001: clean generation must never target handwritten parents.
func TestREQ001GenerationCleanBoundaries(t *testing.T) {
	for _, tc := range []struct {
		file    string
		outputs map[string]bool
	}{
		{"connect-rpc/buf.gen.yaml", map[string]bool{"internal/gen": true, "internal/cli/gen": true}},
		{"connect-rpc/buf.gen.ts.yaml", map[string]bool{"tests/engine/gen": true}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			var config struct {
				Clean   bool `yaml:"clean"`
				Plugins []struct {
					Out string `yaml:"out"`
				} `yaml:"plugins"`
			}
			readTaskYAML(t, tc.file, &config)
			if !config.Clean {
				t.Fatal("generation must clean stale output")
			}
			seen := map[string]bool{}
			for _, plugin := range config.Plugins {
				if !tc.outputs[plugin.Out] {
					t.Errorf("unsafe generation output: %s", plugin.Out)
				}
				seen[plugin.Out] = true
			}
			for output := range tc.outputs {
				if !seen[output] {
					t.Errorf("generated tree is missing: %s", output)
				}
			}
		})
	}
}

func taskPlan(t *testing.T, name string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "task", "--dry", name)
	cmd.Dir = repositoryRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("task --dry %s: %v\n%s", name, err, output)
	}
	return string(output)
}

func requireTaskOrder(t *testing.T, plan string, commands ...string) {
	t.Helper()
	for _, command := range commands {
		index := strings.Index(plan, command)
		if index < 0 {
			t.Fatalf("missing or out-of-order command %q in remaining plan:\n%s", command, plan)
		}
		plan = plan[index+len(command):]
	}
}

func readTaskYAML(t *testing.T, name string, target any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), name))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
