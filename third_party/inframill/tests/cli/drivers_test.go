package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SPEC-0007 REQ-003/004 / AC-003: a real CLI probes configured aliases using
// the debug subprocess protocol and refreshes health on each local invocation.
func TestREQ003LocalDriverHealth(t *testing.T) {
	root := t.TempDir()
	wrapper := filepath.Join(root, "wrapper")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("handlers: [{type: connect-engine}]\nengine: {dataDir: ./data, drivers: {z-missing: {path: ./missing}, a-alias: {path: ./wrapper, args: [%q, driver, debug]}, b-alias: {path: %q, args: [driver, debug]}}}\n", binary, binary)
	if err := os.WriteFile(filepath.Join(root, ".inframill.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var first time.Time
	for _, executable := range []bool{true, false, true} {
		mode := os.FileMode(0o600)
		if executable {
			mode = 0o700
		}
		if err := os.Chmod(wrapper, mode); err != nil {
			t.Fatal(err)
		}
		got := invoke(t, t.Context(), root, "", "driver", "list", "-o", "json")
		var result struct {
			Drivers []struct {
				ID, Name, Version, Status, Reason string
				CheckedAt                         time.Time
			}
		}
		if err := json.Unmarshal([]byte(got.stdout), &result); got.code != 0 || got.stderr != "" || err != nil || len(result.Drivers) != 3 {
			t.Fatal(got, err)
		}
		a, b, missing := result.Drivers[0], result.Drivers[1], result.Drivers[2]
		if a.ID != "a-alias" || b.ID != "b-alias" || missing.ID != "z-missing" || missing.Status != "offline" || missing.Reason == "" {
			t.Fatal(result)
		}
		if b.Name != "debug" || b.Version != "0.0.0-dev" || b.Status != "connected" || b.Reason != "" {
			t.Fatal(b)
		}
		if executable {
			if a.Name != b.Name || a.Version != b.Version || a.Status != "connected" || a.Reason != "" {
				t.Fatal(a)
			}
		} else if a.Status != "offline" || a.Reason == "" || a.Name != "" || a.Version != "" {
			t.Fatal(a)
		}
		if a.CheckedAt.IsZero() || !a.CheckedAt.After(first) {
			t.Fatal("local health was not freshly probed", a.CheckedAt, first)
		}
		first = a.CheckedAt
		if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
			t.Fatal("listing accessed storage", err)
		}
	}
	got := invoke(t, t.Context(), root, "", "driver", "list")
	for _, field := range []string{"ID", "NAME", "VERSION", "STATUS", "REASON", "CHECKED AT", "a-alias", "debug", "connected", "offline"} {
		if got.code != 0 || !strings.Contains(got.stdout, field) {
			t.Fatal(field, got)
		}
	}
}
