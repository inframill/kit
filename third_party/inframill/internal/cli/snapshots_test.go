package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-0002 AC-002: focused help snapshots and byte-exact completion digests.
func TestREQ003HelpAndCompletionSnapshots(t *testing.T) {
	directory, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if writeErr := os.WriteFile(".inframill.yaml", []byte("invalid: ["), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"root-help", []string{helpFlag}},
		{"command-help", []string{"command", helpFlag}},
		{"command-execution-help", []string{"command", "execution", helpFlag}},
		{"driver-list-help", []string{"driver", "list", helpFlag}},
		{"instance-list-help", []string{instanceCommand, listCommand, helpFlag}},
	} {
		want, readErr := os.ReadFile(filepath.Join(directory, tc.name+".txt"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		code, out, errout := invoke(t, &testClient{}, nil, false, tc.args...)
		if code != 0 || out != string(want) || errout != "" {
			t.Fatalf("%s: %d\n%s\n%s", tc.name, code, out, errout)
		}
	}
	for _, shell := range []string{bashShell, zshShell, fishShell, powershellShell} {
		want, readErr := os.ReadFile(filepath.Join(directory, "completion-"+shell+".sha256"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		code, out, errout := invoke(t, &testClient{}, nil, true, completionCommand, shell, alwaysColorFlag)
		digest := sha256.Sum256([]byte(out))
		if code != 0 || errout != "" || hex.EncodeToString(digest[:]) != strings.TrimSpace(string(want)) {
			t.Fatalf("%s completion changed: %d %s", shell, code, errout)
		}
	}
}
