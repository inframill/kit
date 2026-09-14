package cli_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// SPEC-0000 REQ-009/010/018: validation needs no credentials; other failures explain why.
func TestLocalValidationWithoutSecrets(t *testing.T) {
	f := localFiles(t, "success")
	command := f.command(t.Context(), f.args("debug.echo", "validation.json")...)
	command.Env = []string{"PATH=" + f.bin + string(os.PathListSeparator) + os.Getenv("PATH")}
	got := localRun(t, command)
	if got.code != 0 || !json.Valid([]byte(got.stdout)) || got.stderr != "" {
		t.Fatal(got)
	}
	r := readLocalResult(t, f.root+"/validation.json")
	if r.Command == "" || !strings.Contains(string(r.Result), `"values"`) {
		t.Fatal("validation outcome not recorded", r)
	}
	command = f.command(t.Context(), f.args("debug.secret", "missing-secret.json")...)
	command.Env = []string{"PATH=" + f.bin + string(os.PathListSeparator) + os.Getenv("PATH")}
	got = localRun(t, command)
	if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, `secret "test_token" is unavailable`) || !strings.Contains(got.stderr, "COMMAND_FAILED") {
		t.Fatal("missing secret cause hidden", got)
	}
	failure := readLocalResult(t, f.root+"/missing-secret.json")
	if failure.Command == r.Command || !strings.Contains(string(failure.Error), "unavailable") {
		t.Fatal("failed result changed", failure)
	}
}
