package appconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sir-hassan/inframill/pkg/driver"
)

func configFile(t *testing.T, settings string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), DefaultFile)
	data := "handlers:\n  - type: connect-engine\n    settings: {path: /inframill.v1.EngineService/}\nengine: {" + settings + "}\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// SPEC-0000 REQ-019: typed settings preserve path and environment semantics.
func TestDataDirectorySettings(t *testing.T) {
	for _, tc := range []struct{ setting, want string }{
		{"", DefaultDataDir},
		{`dataDir: ""`, DefaultDataDir},
		{`dataDir: ./data`, "data"},
		{fmt.Sprintf("dataDir: %q", t.TempDir()), ""},
		{`dataDir: "${TEST_DATA_DIR}"`, "configured"},
	} {
		path := configFile(t, tc.setting)
		loaded, err := Load(t.Context(), path, []string{"TEST_DATA_DIR=configured"})
		if err != nil {
			t.Fatal(tc, err)
		}
		if tc.want != "" && loaded.DataDir != tc.want {
			t.Fatal(tc, loaded.DataDir)
		}
		if tc.want == "" && !filepath.IsAbs(loaded.DataDir) {
			t.Fatal("absolute dataDir became relative", loaded.DataDir)
		}
	}
}

// SPEC-0000 REQ-019: selected configuration is always required.
func TestRequiredConfigSelection(t *testing.T) {
	missing := filepath.Join(t.TempDir(), DefaultFile)
	for _, path := range []string{missing, ""} {
		if _, err := Load(t.Context(), path, nil); err == nil {
			t.Fatal("missing or empty config accepted")
		}
	}
	path := configFile(t, `dataDir: selected`)
	if root, err := Load(t.Context(), path, nil); err != nil || root.DataDir != "selected" {
		t.Fatal(root, err)
	}
}

// SPEC-0000 REQ-019: reject malformed settings without leaking their values.
func TestInvalidDataConfiguration(t *testing.T) {
	for _, setting := range []string{
		`dataDir: 42`, `dataDir: []`, `dataDir: {private: secret-sentinel}`,
		`dataDir: "   "`, `dataDir: "\0"`, `unknown: secret-sentinel`,
		`dataDir: "${MISSING_DATA_DIR}"`, `dataDir: first, dataDir: secret-sentinel`,
	} {
		path := configFile(t, setting)
		_, err := Load(t.Context(), path, nil)
		if err == nil || strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatal("invalid config accepted or leaked", setting, err)
		}
	}
	for _, content := range []string{"", "invalid: [secret-sentinel", "handlers: []", "handlers: [{type: unknown}]", "---\nhandlers: []\n---\nsecret-sentinel"} {
		path := configFile(t, "")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(t.Context(), path, nil); err == nil || strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatal("invalid YAML accepted or leaked", err)
		}
	}
}

// SPEC-0000 REQ-019: executable selection is explicit and config-relative.
func TestDriverConfiguration(t *testing.T) {
	path := configFile(t, `drivers: {custom: {path: ./tools/custom-driver, args: [driver, debug]}, other: {path: /opt/example/driver}}`)
	config, err := Load(t.Context(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Drivers) != 2 || len(config.Drivers["custom"].Args) != 2 || config.Drivers["custom"].Args[0] != "driver" || config.Drivers["custom"].Args[1] != "debug" || config.Drivers["custom"].Path != filepath.Join(filepath.Dir(path), "tools/custom-driver") || config.Drivers["other"].Path != "/opt/example/driver" {
		t.Fatal(config.Drivers)
	}
	for _, setting := range []string{`drivers: [""]`, `drivers: ["  "]`, `drivers: [42]`, `drivers: {secret: sentinel}`, `drivers: {custom: {path: driver, extra: sentinel}}`, `drivers: {builtin: {path: driver}}`, `drivers: {Bad: {path: driver}}`, `drivers: {custom: {path: driver, env: {KEY: sentinel}}}`} {
		_, err := Load(t.Context(), configFile(t, setting), nil)
		if err == nil || strings.Contains(err.Error(), "sentinel") {
			t.Fatal("invalid Driver config accepted or leaked", err)
		}
	}
	for _, setting := range []string{"", `drivers: {}`} {
		config, err := Load(t.Context(), configFile(t, setting), nil)
		if err != nil || len(config.Drivers) != 0 {
			t.Fatal("implicit drivers configured", config, err)
		}
	}
}

// SPEC-0002 REQ-013 / AC-009: the maintainer's harness section hook composes
// CLI settings without changing native environment resolution or handler checks.
func TestREQ013CombinedApplicationConfiguration(t *testing.T) {
	path := configFile(t, `dataDir: "${TEST_DATA_DIR}"`)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{
		"cli: {endpoint: 'http://example.test', timeout: 2m, output: json, color: auto}",
		"cli: {output: '${CLI_OUTPUT}'}",
	} {
		if err := os.WriteFile(path, append(append([]byte{}, original...), []byte(section+"\n")...), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, loadErr := Load(t.Context(), path, []string{"TEST_DATA_DIR=selected", "CLI_OUTPUT=json"})
		if loadErr != nil || loaded.DataDir != "selected" {
			t.Fatal(loaded, loadErr)
		}
	}
	for _, section := range []string{"cli: {unknown: secret-sentinel}", "cli: {output: 42}", "cli: {output: '${MISSING}'}", "cli: []"} {
		if err := os.WriteFile(path, append(append([]byte{}, original...), []byte(section+"\n")...), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, loadErr := Load(t.Context(), path, []string{"TEST_DATA_DIR=selected"}); loadErr == nil || strings.Contains(loadErr.Error(), "sentinel") {
			t.Fatal(loadErr)
		}
	}
	if err := os.WriteFile(path, []byte("cli: {output: json}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, loadErr := Load(t.Context(), path, nil); loadErr == nil {
		t.Fatal("CLI-only config accepted for native execution")
	}
}

// SPEC-0006 REQ-002/003: bad executable paths are health failures, not YAML failures.
func TestREQ003InvalidPathsRemainConfigured(t *testing.T) {
	for _, setting := range []string{`drivers: {offline: {path: ""}}`, `drivers: {offline: {path: "\0"}}`, `drivers: {offline: {path: ./missing}}`} {
		config, err := Load(t.Context(), configFile(t, setting), nil)
		if err != nil {
			t.Fatal(err)
		}
		manager, err := driver.New(t.Context(), config.Drivers, nil)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := manager.List(t.Context())
		if err != nil || len(entries) != 1 || entries[0].ID != "offline" || entries[0].Status != driver.Offline || entries[0].Reason == "" {
			t.Fatal(entries, err)
		}
	}
}

// SPEC-0010 REQ-001/002: defaults apply with or without the engine object.
func TestREQ001EngineSectionDefaults(t *testing.T) {
	for _, section := range []string{"", "engine: {}\n"} {
		path := configFile(t, "")
		content := "handlers: [{type: connect-engine, settings: {path: /inframill.v1.EngineService/}}]\n" + section
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(t.Context(), path, nil)
		if err != nil || loaded.DataDir != DefaultDataDir || len(loaded.Drivers) != 0 {
			t.Fatal(loaded, err)
		}
	}
}

// SPEC-0010 REQ-002: engine paths and literal args retain native env resolution.
func TestREQ002EngineDriverEnvironment(t *testing.T) {
	path := configFile(t, `drivers: {custom: {path: "${DRIVER_PATH}", args: ["${DRIVER_ARG}", "literal argument"]}}`)
	loaded, err := Load(t.Context(), path, []string{"DRIVER_PATH=./tools/custom", "DRIVER_ARG=debug"})
	if err != nil {
		t.Fatal(err)
	}
	executable := loaded.Drivers["custom"]
	if executable.Path != filepath.Join(filepath.Dir(path), "tools/custom") || len(executable.Args) != 2 || executable.Args[0] != "debug" || executable.Args[1] != "literal argument" {
		t.Fatal(executable)
	}
}

// SPEC-0010 REQ-003: malformed engine and old/mixed handler fields cannot fall back.
func TestREQ003RejectEngineLayouts(t *testing.T) {
	for _, section := range []string{
		"engine: null", "engine: []", "engine: secret-sentinel", "engine: 42",
		"engine: {}\nengine: {}", "engine: {path: secret-sentinel}",
		"engine: {drivers: {}, drivers: {}}",
		"engine: {drivers: {custom: {}, custom: {}}}",
		"engine: {drivers: {custom: {path: first, path: secret-sentinel}}}",
		"engine: {drivers: {custom: {path: 42}}}",
		"engine: {drivers: {custom: {args: [42]}}}",
		"engine: {drivers: {custom: {args: secret-sentinel}}}",
		"engine: {drivers: {custom: {path: '${MISSING_DRIVER}'}}}",
	} {
		t.Run(section, func(t *testing.T) {
			path := configFile(t, "")
			content := "handlers: [{type: connect-engine}]\n" + section
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(t.Context(), path, nil); err == nil || strings.Contains(err.Error(), "sentinel") {
				t.Fatal("invalid engine accepted or leaked", err)
			}
		})
	}
	for _, legacy := range []string{`dataDir: secret-sentinel`, `drivers: {}`, `unknown: secret-sentinel`} {
		for _, engine := range []string{"", "engine: {dataDir: selected, drivers: {}}\n"} {
			path := configFile(t, "")
			content := "handlers: [{type: connect-engine, settings: {path: /inframill.v1.EngineService/, " + legacy + "}}]\n" + engine
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(t.Context(), path, nil); err == nil || strings.Contains(err.Error(), "sentinel") {
				t.Fatal("legacy handler accepted or leaked", err)
			}
		}
	}
}
