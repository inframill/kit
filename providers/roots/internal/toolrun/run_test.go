package toolrun

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialBoundaryAndFilteredChunkedLogs(t *testing.T) {
	secrets := map[string]string{"cloud-token": "a private\ncredential", "unused": "unrelated-secret"}
	t.Setenv("INFRAMILL_SECRET_CLOUD", "not-forwarded")
	t.Setenv("TF_CLI_ARGS", "-destroy")
	env, e := Credentials(map[string]string{"HCLOUD_TOKEN": "cloud-token"}, secrets)
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "HCLOUD_TOKEN=a private\ncredential") || strings.Contains(joined, "not-forwarded") || strings.Contains(joined, "TF_CLI_ARGS") || strings.Contains(joined, "unrelated-secret") {
		t.Fatal("credential boundary")
	}
	var out bytes.Buffer
	f := newFilter(&out, secrets)
	input := "start a private\ncredential a private\\ncredential unrelated-secret end"
	for _, c := range []byte(input) {
		if _, e = f.Write([]byte{c}); e != nil {
			t.Fatal(e)
		}
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	if out.String() != "start [REDACTED] [REDACTED] [REDACTED] end" {
		t.Fatal(out.String())
	}
	if _, e = Credentials(map[string]string{"HCLOUD_TOKEN": "absent"}, secrets); e == nil {
		t.Fatal("missing secret accepted")
	}
	if e = Public(map[string]any{"value": secrets["cloud-token"]}, secrets); e == nil {
		t.Fatal("credential result accepted")
	}
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestRunnerDoesNotLoseOutputFailures(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "tool")
	os.WriteFile(tool, []byte("#!/bin/sh\nprintf hello\n"), 0700)
	r := Runner{Tool: tool, Directory: t.TempDir(), Environment: Environment(), Logs: failing{}}
	if _, _, e := r.Call(t.Context(), false); e == nil {
		t.Fatal("log failure ignored")
	}
}

func TestProgressIsNotBufferedBehindUnrelatedSecrets(t *testing.T) {
	var out bytes.Buffer
	f := newFilter(&out, map[string]string{"token": "a-long-credential-value"})
	f.Write([]byte("task started\n"))
	if out.String() != "task started\n" {
		t.Fatal("progress was delayed")
	}
	f.Write([]byte("a-long-"))
	if out.String() != "task started\n" {
		t.Fatal("partial secret leaked")
	}
	f.Write([]byte("credential-value\ndone\n"))
	if out.String() != "task started\n[REDACTED]\ndone\n" {
		t.Fatal(out.String())
	}
}
