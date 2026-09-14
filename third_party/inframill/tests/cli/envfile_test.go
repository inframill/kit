package cli_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const (
	noEnvFileMode    = "no-flag"
	workspaceEnvMode = "workspace"
	shellEnvMode     = "shell"
	envFileFlag      = "--env-file"
	emptyShellMode   = "empty-shell"
	localSucceeded   = "succeeded"
	localRejected    = "rejected"
)

// SPEC-0000 REQ-020: real CLI composition with a disposable fake tool.
func TestLocalEnvFileCredentials(t *testing.T) {
	for _, mode := range []string{"file", shellEnvMode, emptyShellMode, noEnvFileMode} {
		t.Run(mode, func(t *testing.T) {
			f := localFiles(t, "success")
			fileSecret := "fixture-file-secret"
			envPath := filepath.Join(f.root, ".env")
			data := "INFRAMILL_SECRET_TEST_TOKEN='" + fileSecret + "'\nUNRELATED=private-unused-value\nPATH=/nonexistent\n"
			if err := os.WriteFile(envPath, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			expected := fileSecret
			if mode == shellEnvMode {
				expected = "fixture-secret"
			}
			args := f.args("debug.secret", "result.json")
			if mode != noEnvFileMode {
				args = append(args, envFileFlag, ".env") // Relative to CLI cwd.
			}
			command := f.command(t.Context(), args...)
			if mode != shellEnvMode {
				command.Env = command.Env[:1]
			}
			if mode == emptyShellMode {
				command.Env = append(command.Env, "INFRAMILL_SECRET_TEST_TOKEN=")
			}
			got := localRun(t, command)
			r := readLocalResult(t, filepath.Join(f.root, "result.json"))
			if mode == emptyShellMode || mode == noEnvFileMode {
				if got.code != 1 || got.stdout != "" || r.Status != "failed" || !strings.Contains(got.stderr, "is unavailable") {
					t.Fatal("unexpected missing-secret behavior", got, r)
				}
				return
			}
			if got.code != 0 || !json.Valid([]byte(got.stdout)) || got.stderr != "[REDACTED]\n" || r.Status != localSucceeded {
				t.Fatal("file secret not resolved or redacted", got, r)
			}
			history := localRun(t, f.command(t.Context(), "history", "--workspace", f.workspace, "--output", "json"))
			if history.code != 0 || strings.Contains(history.stdout, fileSecret) || strings.Contains(history.stdout, expected) {
				t.Fatal("history exposed secret")
			}
			if bytes.Contains(r.Error, []byte(expected)) || bytes.Contains(r.Result, []byte(expected)) {
				t.Fatal("result exposed secret")
			}
			if err := filepath.WalkDir(f.workspace, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return nil
				}
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				for _, forbidden := range []string{expected, fileSecret, "private-unused-value"} {
					if bytes.Contains(content, []byte(forbidden)) {
						t.Fatal("secret persisted in workspace", path)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// SPEC-0000 REQ-020: file errors precede workspace admission and export safely.
func TestLocalEnvFileRejections(t *testing.T) {
	for _, mode := range []string{"empty", missingInput, directoryInput, symlinkCollision, "fifo", "unreadable", oversizedInput, malformedInput, "inside-blueprint", workspaceEnvMode} {
		t.Run(mode, func(t *testing.T) {
			f := localFiles(t, "success")
			path := filepath.Join(f.root, ".env")
			content := "INFRAMILL_SECRET_TEST_TOKEN=fixture-file-secret\n"
			switch mode {
			case "empty":
				path = ""
			case missingInput:
			case directoryInput:
				path = f.bin
			case symlinkCollision:
				if err := os.Symlink(f.instance, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				switch mode {
				case oversizedInput:
					content = strings.Repeat("#", (1<<20)+1)
				case malformedInput:
					content += "fixture-sensitive-invalid-line\n"
				case "inside-blueprint":
					path = filepath.Join(f.blueprint, ".env")
				case workspaceEnvMode:
					if err := os.MkdirAll(f.workspace, 0o700); err != nil {
						t.Fatal(err)
					}
					path = filepath.Join(f.workspace, ".env")
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				if mode == "unreadable" {
					if os.Geteuid() == 0 {
						t.Skip("root can read mode-000 files")
					}
					if err := os.Chmod(path, 0); err != nil {
						t.Fatal(err)
					}
				}
			}
			args := append(f.args("debug.secret", "rejected.json"), envFileFlag, path)
			got := localRun(t, f.command(t.Context(), args...))
			r := readLocalResult(t, filepath.Join(f.root, "rejected.json"))
			if got.code != 2 || got.stdout != "" || r.Status != localRejected || r.Command != "" || !bytes.Contains(r.Error, []byte("LOCAL_ERROR")) {
				t.Fatal("env file failure did not reject safely", got, r)
			}
			if strings.Contains(got.stderr, "fixture-") || bytes.Contains(r.Error, []byte("fixture-")) {
				t.Fatal("environment contents exposed")
			}
			if mode == workspaceEnvMode {
				entries, err := os.ReadDir(f.workspace)
				if err != nil || len(entries) != 1 || entries[0].Name() != ".env" {
					t.Fatal("existing workspace altered", err)
				}
			} else if _, err := os.Stat(f.workspace); !os.IsNotExist(err) {
				t.Fatal("workspace created before env file rejection", err)
			}
		})
	}
}

// SPEC-0000 REQ-020: validate parses explicit files without resolving secrets.
func TestLocalEnvFileLiteralValidation(t *testing.T) {
	f := localFiles(t, "success")
	path := filepath.Join(f.root, ".env")
	// Expansion would create a file in the CLI cwd, even though validate needs no secret.
	if err := os.WriteFile(path, []byte("INFRAMILL_SECRET_TEST_TOKEN=$(touch sentinel)`touch sentinel`\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append(f.args("debug.echo", ""), envFileFlag, path)
	command := f.command(t.Context(), args...)
	command.Env = command.Env[:1]
	got := localRun(t, command)
	if got.code != 0 || !json.Valid([]byte(got.stdout)) || got.stderr != "" {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(f.root, "sentinel")); !os.IsNotExist(err) {
		t.Fatal("environment file executed shell syntax", err)
	}
}
