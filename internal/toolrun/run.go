// Package toolrun executes tools without inheriting arbitrary user credentials.
package toolrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	driver "github.com/sir-hassan/inframill/pkg/driver"
)

const Limit = 32 << 20

type Runner struct {
	Tool, Directory string
	Environment     []string
	Logs            io.Writer
	Secrets         driver.Secrets
}

func Environment() []string {
	out := []string{"LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1", "ANSIBLE_NOCOLOR=1"}
	for _, key := range []string{"HOME", "PATH", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR", "SSH_AUTH_SOCK"} {
		if v, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+v)
		}
	}
	return out
}
func Credentials(names map[string]string, secrets driver.Secrets) ([]string, error) {
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := Environment()
	for _, key := range keys {
		value, ok := secrets[names[key]]
		if !ok || value == "" {
			return nil, &driver.Error{Code: "MISSING_SECRET", Message: names[key] + " is required"}
		}
		env = append(env, key+"="+value)
	}
	return env, nil
}

type bounded struct{ bytes.Buffer }

func (b *bounded) Write(p []byte) (int, error) {
	if b.Len()+len(p) > Limit {
		return 0, errors.New("tool output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func (r Runner) Call(ctx context.Context, capture bool, args ...string) ([]byte, int, error) {
	if !filepath.IsAbs(r.Tool) {
		return nil, -1, errors.New("tool path must be absolute")
	}
	logs := r.Logs
	if logs == nil {
		logs = io.Discard
	}
	filter := newFilter(logs, r.Secrets)
	cmd := exec.CommandContext(ctx, r.Tool, args...)
	cmd.Dir = r.Directory
	cmd.Env = r.Environment
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = nil
	cmd.Stderr = filter
	var output bounded
	if capture {
		cmd.Stdout = &output
	} else {
		cmd.Stdout = filter
	}
	err := cmd.Run()
	flush := filter.Close()
	if flush != nil {
		return nil, -1, errors.New("cannot write tool diagnostics")
	}
	if ctx.Err() != nil {
		return nil, -1, ctx.Err()
	}
	if err == nil {
		return output.Bytes(), 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return output.Bytes(), exit.ExitCode(), nil
	}
	return nil, -1, errors.New("tool process or output failed")
}
func Success(code int, err error) error {
	if err != nil {
		return err
	}
	if code != 0 {
		return &driver.Error{Code: "TOOL_FAILED", Message: "tool failed; see diagnostics"}
	}
	return nil
}
func (r Runner) Verify(ctx context.Context, name, version string) error {
	args := []string{"version", "-json"}
	if name == "ansible" {
		args = []string{"--version"}
	}
	data, code, err := r.Call(ctx, true, args...)
	if err = Success(code, err); err != nil {
		return err
	}
	if name == "opentofu" {
		var result struct {
			Version string `json:"terraform_version"`
		}
		if json.Unmarshal(data, &result) == nil && result.Version == version {
			return nil
		}
	} else if bytes.Contains(data, []byte("[core "+version+"]")) {
		return nil
	}
	return &driver.Error{Code: "TOOL_VERSION", Message: "tool does not match release lock"}
}
func Public(value any, secrets driver.Secrets) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	for _, pattern := range patterns(secrets) {
		if bytes.Contains(data, pattern) {
			return &driver.Error{Code: "SECRET_OUTPUT", Message: "refusing credential-bearing output"}
		}
	}
	return nil
}
