package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
)

func noDriverClient(string) inframillv1connect.EngineServiceClient {
	panic("local Driver listing must not construct an RPC client")
}

// SPEC-0007 REQ-003/004 / AC-003: native listing ignores remote settings and
// succeeds even when the data root cannot contain Instance storage.
func TestREQ003NativeDriverList(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("data", []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := config(t, "handlers: [{type: connect-engine}]\nengine: {dataDir: data, drivers: {missing: {path: ./absent}}}\ncli: {endpoint: invalid, timeout: invalid, output: json, color: always}\n")
	var stdout, stderr bytes.Buffer
	d := Dependencies{Stdout: &stdout, Stderr: &stderr, Client: noDriverClient, Environment: []string{"INFRAMILL_ENDPOINT=invalid", "INFRAMILL_TIMEOUT=invalid"}}
	args := []string{"driver", "list", "--config", path}
	code := Run(t.Context(), args, d)
	var response struct {
		Drivers []struct {
			ID, Name, Version, Status, Reason string
			CheckedAt                         time.Time
		}
	}
	if code != 0 || stderr.Len() != 0 || strings.Contains(stdout.String(), "\x1b") {
		t.Fatal(code, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || len(response.Drivers) != 1 {
		t.Fatal(stdout.String(), err)
	}
	entry := response.Drivers[0]
	if entry.ID != "missing" || entry.Status != "offline" || entry.Reason == "" || entry.Name != "" || entry.Version != "" || entry.CheckedAt.IsZero() {
		t.Fatal(entry)
	}
	stdout.Reset()
	code = Run(t.Context(), append(args, "-o", "table", "--columns", "status,id", "--color", "never"), d)
	if code != 0 || !strings.Contains(stdout.String(), "STATUS") || !strings.Contains(stdout.String(), "offline") || strings.Contains(stdout.String(), "REASON") {
		t.Fatal(code, stdout.String(), stderr.String())
	}
	if strings.Index(stdout.String(), "STATUS") > strings.Index(stdout.String(), "ID") {
		t.Fatal("column order changed", stdout.String())
	}
	data, err := os.ReadFile("data")
	if err != nil || string(data) != "preserve" {
		t.Fatal("storage changed", string(data), err)
	}
}

// SPEC-0007 REQ-004 / AC-003: empty results, invalid input, output errors,
// cancellation and offline utilities keep the established exit classes.
func TestREQ004DriverListFailuresAndUtilities(t *testing.T) {
	t.Chdir(t.TempDir())
	path := config(t, "handlers: [{type: connect-engine}]\nengine: {drivers: {}}\n")
	for _, tc := range []struct {
		args []string
		code int
		text string
	}{
		{[]string{"--config", path}, 0, "No drivers configured."},
		{[]string{"--config", path, "-o", "json"}, 0, `{"drivers":[]}`},
		{nil, 2, ""},
		{[]string{"--config", "missing"}, 2, ""},
		{[]string{"--config="}, 2, ""},
		{[]string{"--config", config(t, "cli: {}")}, 2, ""},
		{[]string{"--config", config(t, "invalid: [")}, 2, ""},
		{[]string{"--config", path, "--endpoint", "http://localhost:1"}, 2, ""},
		{[]string{"--config", path, "--timeout", "1s"}, 2, ""},
		{[]string{"--config", path, "--output="}, 2, ""},
		{[]string{"--config", path, "--output=yaml"}, 2, ""},
		{[]string{"--config", path, "--columns="}, 2, ""},
		{[]string{"--config", path, "--columns=id,id"}, 2, ""},
		{[]string{"--config", path, "--columns=unknown"}, 2, ""},
		{[]string{"--config", path, "--columns=id", "-o=json"}, 2, ""},
		{[]string{"--config", path, "extra"}, 2, ""},
	} {
		var out, errout bytes.Buffer
		code := Run(t.Context(), append([]string{"driver", "list"}, tc.args...), Dependencies{Stdout: &out, Stderr: &errout, Client: noDriverClient})
		if code != tc.code || (tc.code == 0 && !strings.Contains(out.String(), tc.text)) || (tc.code != 0 && out.Len() != 0) {
			t.Fatal(tc.args, code, out.String(), errout.String())
		}
	}
	for _, format := range []string{"table", "json"} {
		code := Run(t.Context(), []string{"driver", "list", "--config", path, "-o", format}, Dependencies{Stdout: brokenWriter{}, Client: noDriverClient})
		if code != 3 {
			t.Fatal(format, code)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if code := Run(ctx, []string{"driver", "list", "--config", path}, Dependencies{Client: noDriverClient}); code != 130 {
		t.Fatal("cancellation", code)
	}
	if err := os.WriteFile(".inframill.yaml", []byte("invalid: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"driver", "list", "--help"}, {"help", "driver", "list"}, {"__complete", "driver", "list", "--output", ""}} {
		var out bytes.Buffer
		code := Run(t.Context(), args, Dependencies{Stdout: &out, Stderr: io.Discard, Client: noDriverClient})
		if code != 0 || out.Len() == 0 {
			t.Fatal(args, code, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(".inframill", "instances")); !os.IsNotExist(err) {
		t.Fatal("listing created storage", err)
	}
}

// SPEC-0007 REQ-003/004, NFR-001: failed probe diagnostics retain secret
// redaction, and caller cancellation stops an in-flight probe.
func TestREQ004DriverListProbeBoundaries(t *testing.T) {
	t.Chdir(t.TempDir())
	const secret = "synthetic-driver-health-secret"
	for _, tc := range []struct {
		name, script string
		cancel       bool
	}{
		{"redaction", "#!/bin/sh\nprintf '" + secret + "' >&2\nexit 1\n", false},
		{"cancellation", "#!/bin/sh\nexec /bin/sleep 30\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), "driver")
			if err := os.WriteFile(executable, []byte(tc.script), 0o700); err != nil {
				t.Fatal(err)
			}
			path := config(t, fmt.Sprintf("handlers: [{type: connect-engine}]\nengine: {drivers: {fixture: {path: %q}}}\n", executable))
			var out, errout bytes.Buffer
			ctx := t.Context()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			}
			start := time.Now()
			code := Run(ctx, []string{"driver", "list", "--config", path, "-o", "json"}, Dependencies{Stdout: &out, Stderr: &errout, Client: noDriverClient, Environment: []string{"INFRAMILL_SECRET_TEST=" + secret}})
			if tc.cancel {
				if code != 130 || out.Len() != 0 || time.Since(start) > 5*time.Second {
					t.Fatal(code, out.String(), errout.String())
				}
			} else if code != 0 || strings.Contains(out.String()+errout.String(), secret) || !strings.Contains(out.String(), "[REDACTED]") {
				t.Fatal(code, out.String(), errout.String())
			}
		})
	}
}

// SPEC-0008 REQ-002 / AC-002: native table shortening must not alter JSON.
func TestREQ002DriverTableKeepsFullJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	const id = "abcdefghijklmnopqrstuvwxyz"
	path := config(t, "handlers: [{type: connect-engine}]\nengine: {drivers: {"+id+": {path: ./missing}}}\n")
	var table, data bytes.Buffer
	for _, tc := range []struct {
		format string
		out    *bytes.Buffer
	}{{"table", &table}, {"json", &data}} {
		if code := Run(t.Context(), []string{"driver", "list", "--config", path, "-o", tc.format}, Dependencies{Stdout: tc.out, Client: noDriverClient}); code != 0 {
			t.Fatal(tc.format, code)
		}
	}
	var response struct {
		Drivers []struct{ ID, Reason string }
	}
	if err := json.Unmarshal(data.Bytes(), &response); err != nil || len(response.Drivers) != 1 {
		t.Fatal(data.String(), err)
	}
	const reason = "driver executable is missing or not executable"
	if response.Drivers[0].ID != id || response.Drivers[0].Reason != reason {
		t.Fatal(data.String())
	}
	if strings.Contains(table.String(), id) || strings.Contains(table.String(), reason) || !strings.Contains(table.String(), "abcdefghijklmnopq...") || !strings.Contains(table.String(), "driver executable...") {
		t.Fatal(table.String())
	}
}
