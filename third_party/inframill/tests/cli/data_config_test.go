package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
)

const (
	applicationConfigFlag = "--config"
	emptyConfigOverride   = "empty-override"
	missingDefaultConfig  = "missing-default"
)

// SPEC-0000 REQ-019: both entry points require the selected file, with no fallback.
func TestRequiredApplicationConfig(t *testing.T) {
	for _, mode := range []string{localExecCommand, argServer} {
		for _, kind := range []string{missingDefaultConfig, "legacy-name", "missing-override", emptyConfigOverride, "invalid-default"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				f := localFiles(t, "success")
				config := filepath.Join(f.root, ".inframill.yaml")
				args := []string{argServer}
				if mode == localExecCommand {
					args = f.args("debug.echo", "not-created.json")
				}
				switch kind {
				case missingDefaultConfig:
					if err := os.Remove(config); err != nil {
						t.Fatal(err)
					}
				case "legacy-name":
					if err := os.Rename(config, filepath.Join(f.root, "inframill.yaml")); err != nil {
						t.Fatal(err)
					}
				case "missing-override":
					args = append(args, applicationConfigFlag, filepath.Join(f.root, "absent.yaml"))
				case emptyConfigOverride:
					args = append(args, applicationConfigFlag+"=")
				case "invalid-default":
					if err := os.WriteFile(config, []byte("invalid: [secret-sentinel"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				got := localRun(t, f.command(ctx, args...))
				wantCode := 2
				if mode == argServer && kind != emptyConfigOverride {
					wantCode = 3
				}
				if got.code != wantCode || got.stdout != "" || strings.Contains(got.stderr, "secret-sentinel") {
					t.Fatal("missing/invalid selected configuration accepted or leaked", got)
				}
				for _, path := range []string{f.workspace, filepath.Join(f.root, "not-created.json")} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatal("configuration failure caused effects", path, err)
					}
				}
				if kind == missingDefaultConfig {
					if help := localRun(t, f.command(ctx, mode, "--help")); help.code != 0 {
						t.Fatal("help requires configuration", help)
					}
				}
			})
		}
	}
}

func writeDataConfig(t *testing.T, path, dataDir string) {
	t.Helper()
	content := fmt.Sprintf(`main: {port: 8080}
logger: {type: slog, settings: {level: info, format: json}}
engine: {dataDir: %q, drivers: {debug: {path: %q, args: [driver, debug]}}}
handlers:
  - type: connect-engine
    settings: {path: /inframill.v1.EngineService/}
workers: []
`, dataDir, binary)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// SPEC-0000 REQ-017/019: removed overrides fail before config/input/result/storage effects.
func TestExecWorkspaceFlagRemoved(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		t.Run(strconv.FormatBool(assigned), func(t *testing.T) {
			f := localFiles(t, "success")
			other := filepath.Join(f.root, "other")
			args := f.args("debug.echo", "rejected.json")
			if assigned {
				args = append(args, workspaceFlag+"="+other)
			} else {
				args = append(args, workspaceFlag, other)
			}
			if got := localRun(t, f.command(t.Context(), args...)); got.code != 2 || !strings.Contains(got.stderr, "unknown local flag") {
				t.Fatal(got)
			}
			for _, path := range []string{f.workspace, other, filepath.Join(f.root, "rejected.json")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("removed flag caused effects", path, err)
				}
			}
		})
	}
}

// SPEC-0000 REQ-019: config-file location does not relocate a relative dataDir.
func TestExecConfiguredDataDirectory(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(strconv.FormatBool(explicit), func(t *testing.T) {
			f := localFiles(t, "success")
			config := filepath.Join(f.root, ".inframill.yaml")
			if explicit {
				config = filepath.Join(t.TempDir(), "selected.yaml")
			}
			writeDataConfig(t, config, "configured-data")
			args := append(f.args("debug.echo", "configured.json"), nameFlag, namedDev)
			if explicit {
				args = append(args, applicationConfigFlag, config)
			}
			if got := localRun(t, f.command(t.Context(), args...)); got.code != 0 {
				t.Fatal(got)
			}
			r := readLocalResult(t, filepath.Join(f.root, "configured.json"))
			if _, err := os.Stat(f.workspace); !os.IsNotExist(err) {
				t.Fatal("configured exec created default root", err)
			}
			f.workspace = filepath.Join(f.root, "configured-data")
			page := readSelectionHistory(t, f, namedDev)
			if len(page.Executions) != 1 || page.Executions[0].Instance != r.Instance {
				t.Fatal("history did not select configured root", page)
			}
			if explicit {
				if _, err := os.Stat(filepath.Join(filepath.Dir(config), "configured-data")); !os.IsNotExist(err) {
					t.Fatal("dataDir resolved from config location", err)
				}
			}
			base := append(f.args("debug.echo", ""), nameFlag, namedDev)
			if explicit {
				base = append(base, applicationConfigFlag, config)
			}
			protected := slices.Clone(base)
			protected = append(protected, resultFileFlag, filepath.Join(f.workspace, "result.json"))
			if got := localRun(t, f.command(t.Context(), protected...)); got.code != 3 {
				t.Fatal("result entered configured root", got)
			}
			if got := localRun(t, f.command(t.Context(), append(base, "--env-file", filepath.Join(f.workspace, "missing.env"))...)); got.code != 2 || !strings.Contains(got.stderr, "outside") {
				t.Fatal(got)
			}
		})
	}
}

// SPEC-0000 REQ-019: bad config rejects before reservation, storage or tool execution.
func TestExecDataConfigRejections(t *testing.T) {
	for _, kind := range []string{missingInput, "empty-flag", malformedInput, "wrong-type", directoryInput} {
		t.Run(kind, func(t *testing.T) {
			f := localFiles(t, "success")
			path := filepath.Join(f.root, ".inframill.yaml")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			args := f.args("debug.echo", "rejected.json")
			switch kind {
			case missingInput:
				args = append(args, applicationConfigFlag, path)
			case "empty-flag":
				args = append(args, applicationConfigFlag+"=")
			case directoryInput:
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			default:
				data := "invalid: [secret-sentinel"
				if kind == "wrong-type" {
					data = "handlers: [{type: connect-engine}]\nengine: {dataDir: {private: secret-sentinel}}"
				}
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := localRun(t, f.command(t.Context(), args...))
			if got.code != 2 || strings.Contains(got.stderr, "secret-sentinel") {
				t.Fatal(got)
			}
			for _, path := range []string{f.workspace, filepath.Join(f.root, "rejected.json")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("invalid config caused effects", path, err)
				}
			}
		})
	}
}

// SPEC-0000 REQ-019: the real daemon reopens local data and the same verified cache.
func TestConfiguredDataSharedWithDaemon(t *testing.T) {
	f := localFiles(t, "success")
	f.workspace = filepath.Join(t.TempDir(), "shared-data")
	config := filepath.Join(f.root, ".inframill.yaml")
	writeDataConfig(t, config, f.workspace)
	registry, digest := localRegistry(t, f.blueprint)
	host := strings.TrimPrefix(registry.URL, "http://")
	writeInstance(t, f.instance, host+"/fixture:latest")
	command := f.command(t.Context(), localExecCommand, "debug.echo", instanceFlag, f.instance, nameFlag, namedDev, resultFileFlag, filepath.Join(f.root, "local.json"))
	command.Env = append(command.Env, "INFRAMILL_HTTP_REGISTRIES="+host)
	if got := localRun(t, command); got.code != 0 {
		t.Fatal(got)
	}
	local := readLocalResult(t, filepath.Join(f.root, "local.json"))
	registry.Close()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(config, bytes.Replace(data, []byte("port: 8080"), []byte("port: "+port), 1), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	daemon := f.command(ctx, argServer) // SPEC-0000 REQ-019: use the required default config.
	if startErr := daemon.Start(); startErr != nil {
		t.Fatal(startErr)
	}
	defer func() {
		cancel()
		_ = daemon.Wait() //nolint:errcheck // Cleanup deliberately cancels the disposable daemon.
	}()
	client := inframillv1connect.NewEngineServiceClient(&http.Client{Timeout: time.Second}, "http://"+address)
	for {
		response, queryErr := client.GetInstance(ctx, connect.NewRequest(&pb.GetInstanceRequest{InstanceId: local.Instance}))
		if queryErr == nil {
			if response.Msg.GetInstance().GetName() != namedDev {
				t.Fatal("daemon opened wrong Instance", response.Msg)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("daemon did not reopen local Instance", queryErr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	packages, err := client.ListBlueprints(ctx, connect.NewRequest(&pb.ListBlueprintsRequest{}))
	if err != nil || len(packages.Msg.GetBlueprints()) != 1 || packages.Msg.GetBlueprints()[0].GetDigest() != digest {
		t.Fatal("daemon did not use configured cache", packages, err)
	}
	history, err := client.ListCommandExecutions(ctx, connect.NewRequest(&pb.ListCommandExecutionsRequest{InstanceId: local.Instance}))
	if err != nil || len(history.Msg.GetExecutions()) != 1 || history.Msg.GetExecutions()[0].GetRequest().GetId() != local.Command {
		t.Fatal("daemon did not retain local history", history, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, defaultWorkspace)); !os.IsNotExist(err) {
		t.Fatal("configured entry point created default storage", err)
	}
	verifyRemoteWaitCancellation(t, ctx, f, client, "http://"+address, local.Instance)
}

// SPEC-0010 REQ-003 / AC-002: old and mixed settings fail before probes or writes.
func TestREQ003EngineConfigRejectionBeforeEffects(t *testing.T) {
	for _, mode := range []string{localExecCommand, argServer, "driver"} {
		for _, kind := range []string{"legacy-data", "legacy-drivers", "mixed", "invalid-engine"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				f := localFiles(t, "success")
				if err := os.Mkdir(filepath.Join(f.root, "existing"), 0o700); err != nil {
					t.Fatal(err)
				}
				sentinel := filepath.Join(f.root, "existing", "sentinel")
				if err := os.WriteFile(sentinel, []byte("existing data"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(f.root, "probe"), []byte("#!/bin/sh\n: > probe-marker\nexit 1\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				engine := "engine: {dataDir: new-data, drivers: {probe: {path: ./probe}}}\n"
				legacy := ""
				switch kind {
				case "legacy-data":
					engine = ""
					legacy = ", dataDir: existing"
				case "legacy-drivers":
					engine = ""
					legacy = ", drivers: {probe: {path: ./probe}}"
				case "mixed":
					legacy = ", dataDir: existing, drivers: {probe: {path: ./probe}}"
				case "invalid-engine":
					engine = "engine: {dataDir: new-data, unknown: secret-sentinel, drivers: {probe: {path: ./probe}}}\n"
				}
				content := engine + "handlers: [{type: connect-engine, settings: {path: /inframill.v1.EngineService/" + legacy + "}}]\n"
				if err := os.WriteFile(filepath.Join(f.root, ".inframill.yaml"), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				args, wantCode := []string{mode}, 2
				if mode == localExecCommand {
					args = f.args("debug.echo", "rejected.json")
				} else if mode == "driver" {
					args = append(args, "list")
				} else {
					wantCode = 3
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				got := localRun(t, f.command(ctx, args...))
				if got.code != wantCode || got.stdout != "" || strings.Contains(got.stderr, "sentinel") {
					t.Fatal("configuration accepted or leaked", got)
				}
				for _, name := range []string{"probe-marker", "new-data", ".inframill", "rejected.json"} {
					if _, err := os.Stat(filepath.Join(f.root, name)); !os.IsNotExist(err) {
						t.Fatal("configuration failure caused effects", name, err)
					}
				}
				if data, err := os.ReadFile(sentinel); err != nil || string(data) != "existing data" {
					t.Fatal("existing data changed", err)
				}
			})
		}
	}
}
