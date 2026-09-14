//go:build architecture

package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	driversPackage      = "pkg/driver"
	processPackage      = "pkg/driver/internal/process"
	harnessPackage      = "pkg/harness"
	connectPackage      = "internal/connectadapter"
	blueprintPackage    = "internal/blueprint"
	backendPackage      = "internal/backend"
	modulePath          = "github.com/sir-hassan/inframill"
	publicPackage       = "pkg/inframill"
	outputPackage       = "internal/cli/output"
	remoteOutputPackage = "internal/cli/output/remote"
	renderPackage       = "internal/backend/render"
	configPackage       = "internal/appconfig"
)

// SPEC-0000 REQ-001: the unified public API and orchestration cannot depend on
// concrete storage, protocols or drivers.
func TestPackageBoundaries(t *testing.T) {
	root := repositoryRoot(t)
	// SPEC-0000 REQ-013: codecs stay in concrete Drivers; shared paths
	// and models cannot import Driver implementations.
	rules := map[string][]string{
		"internal/cli/localexec":     {driversPackage, configPackage, outputPackage, backendPackage, blueprintPackage, publicPackage},
		configPackage:                {driversPackage, harnessPackage, "pkg/harness/config", "internal/cli/config"},
		outputPackage:                {},
		remoteOutputPackage:          {outputPackage, "internal/gen/inframill/v1"},
		"cmd":                        {"internal/cli"},
		"tests/engine/fixture":       {blueprintPackage},
		"internal/cli":               {"internal/cli/gen", "internal/gen/inframill/v1/inframillv1connect", "internal/cli/runtime", "internal/debugdriver", "internal/cli/localexec", "internal/server"},
		"internal/cli/runtime":       {"internal/gen/inframill/v1", "internal/gen/inframill/v1/inframillv1connect", "internal/cli/config", outputPackage, remoteOutputPackage},
		"internal/cli/config":        {},
		"internal/cli/generator":     {},
		"internal/cli/generator/cmd": {"internal/cli/generator"},
		"internal/cli/gen":           {"internal/gen/inframill/v1", "internal/gen/inframill/v1/inframillv1connect", "internal/cli/runtime"},
		"internal/gen/inframill/v1":  {},
		"internal/gen/inframill/v1/inframillv1connect": {"internal/gen/inframill/v1"},
		"internal/server":            {configPackage, backendPackage, blueprintPackage, connectPackage, driversPackage, harnessPackage, "pkg/harness/logger", publicPackage},
		processPackage:               {},
		harnessPackage:               {"pkg/harness/internal/dispatch", "pkg/harness/internal/httpserver"},
		driversPackage:               {processPackage},
		"internal/debugdriver":       {publicPackage, driversPackage, "internal/platform/secretenv"},
		publicPackage:                {},
		backendPackage:               {publicPackage, driversPackage, renderPackage, "internal/blueprint/content"},
		renderPackage:                {"internal/platform/filetree"},
		"internal/blueprint/content": {"internal/platform/filetree"},
		connectPackage:               {configPackage, publicPackage, harnessPackage, "internal/gen/inframill/v1", "internal/gen/inframill/v1/inframillv1connect"},
		blueprintPackage:             {publicPackage, "internal/blueprint/content", "internal/schema", "internal/platform/filetree"},
	}
	for directory, allowed := range rules {
		t.Run(directory, func(t *testing.T) {
			files, err := filepath.Glob(filepath.Join(root, directory, "*.go"))
			if err != nil {
				t.Fatal(err)
			}
			production := 0
			for _, file := range files {
				if strings.HasSuffix(file, "_test.go") {
					continue
				}
				production++
				for _, imported := range imports(t, file) {
					local, isLocal := strings.CutPrefix(imported, modulePath+"/")
					if !isLocal {
						if strings.HasPrefix(directory, "pkg/") && strings.Contains(imported, ".") {
							t.Errorf("public package imports external implementation %s", imported)
						}
						continue
					}
					if !slices.Contains(allowed, local) {
						t.Errorf("%s imports outside its boundary: %s", file, imported)
					}
				}
			}
			if production == 0 {
				t.Fatal("required package has no production files")
			}
		})
	}
}

// SPEC-0000 REQ-008/010/015: one binary exposes the debug Driver through a subcommand.
func TestSingleCommandEntryPoint(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(repositoryRoot(t), "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "main.go" {
		t.Fatal("cmd must contain only main.go, including the debug Driver dispatch")
	}
}

// SPEC-0006 REQ-001: public Driver mechanics cannot import application packages.
func TestDriverManagerRemainsIndependent(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), driversPackage)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		for _, imported := range imports(t, path) {
			local, owned := strings.CutPrefix(imported, modulePath+"/")
			if owned && !strings.HasPrefix(local, driversPackage+"/") && local != driversPackage {
				t.Errorf("%s imports application package %s", path, imported)
			}
			if !owned && strings.Contains(imported, ".") {
				t.Errorf("%s imports external dependency %s", path, imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// SPEC-0000 REQ-014: harness and its batteries stay independent of Inframill.
func TestHarnessRemainsApplicationNeutral(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), harnessPackage)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		for _, imported := range imports(t, path) {
			local, isLocal := strings.CutPrefix(imported, modulePath+"/")
			if isLocal && local != harnessPackage && !strings.HasPrefix(local, "pkg/harness/") {
				t.Errorf("%s imports application code: %s", path, imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func imports(t *testing.T, file string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, len(parsed.Imports))
	for _, spec := range parsed.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, imported)
	}
	return result
}

// SPEC-0003 REQ-002 / SPEC-0004 REQ-001: CLI ownership has one subtree,
// and obsolete generated output directories must not remain.
func TestCLIPackagesConsolidated(t *testing.T) {
	root := repositoryRoot(t)
	for _, old := range []string{"internal/cliconfig", "internal/cliruntime", "internal/clioutput", "internal/cligen", "internal/localexec", "gen", "internal/cli/generated", "internal/drivers", "internal/driverexec", "internal/platform/process"} {
		if _, err := os.Stat(filepath.Join(root, old)); !os.IsNotExist(err) {
			t.Errorf("obsolete CLI directory %s remains: %v", old, err)
		}
	}
	for _, directory := range []string{"config", "runtime", "output", "output/remote", "localexec", "generator", "gen"} {
		if info, err := os.Stat(filepath.Join(root, "internal/cli", directory)); err != nil || !info.IsDir() {
			t.Errorf("missing CLI subpackage %s", directory)
		}
	}
}

// SPEC-0003 REQ-002: preferences and views remain usable without command parsing.
func TestCLILeavesDoNotImportCobra(t *testing.T) {
	for _, leaf := range []string{"config", "output", "output/remote"} {
		files, err := filepath.Glob(filepath.Join(repositoryRoot(t), "internal/cli", leaf, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			for _, imported := range imports(t, file) {
				if strings.HasPrefix(imported, "github.com/spf13/") {
					t.Errorf("%s imports command parsing: %s", file, imported)
				}
			}
		}
	}
}
