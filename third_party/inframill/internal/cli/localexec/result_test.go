package localexec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResultPublicationPreservesChangedDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	file, err := reserveResult(options{resultFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(path, []byte("user content"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if publishErr := file.publish(result{APIVersion: resultVersion, Status: "rejected"}); publishErr == nil {
		t.Fatal("changed destination overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "user content" {
		t.Fatal(string(data), err)
	}
}

func TestResultDestinationCannotCreateInputFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-input.json")
	if _, err := reserveResult(options{instance: path, resultFile: path}); err == nil {
		t.Fatal("result aliased missing input")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("input file created", err)
	}
}

// SPEC-0000 REQ-018/020: reserving a result must never create a missing env input.
func TestResultDestinationCannotCreateEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.env")
	if _, err := reserveResult(options{envFile: path, resultFile: path}); err == nil {
		t.Fatal("result aliased missing environment input")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("environment input created", err)
	}
}
