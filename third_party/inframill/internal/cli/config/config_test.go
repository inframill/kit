package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-0002 REQ-013 / AC-009: structure is strict while scalar validation is lazy.
func TestREQ013SectionIsolation(t *testing.T) {
	cases := []struct {
		name, text string
		valid      bool
	}{
		{"cli only", "cli: {endpoint: 'http://example.test', timeout: 2m, output: json, color: auto}", true},
		{"unrelated server refs", "main: {port: invalid}\nhandlers: [{settings: {secret: '${MISSING}'}}]\ncli: {output: table}", true},
		{"unrelated engine refs", "engine: {dataDir: '${MISSING}', drivers: {bad: {path: 42}}, unknown: ignored}\ncli: {output: table}", true},
		{"unrelated engine shape", "engine: []\ncli: {output: table}", true},
		{"invalid scalar deferred", "cli: {timeout: bad, endpoint: '', color: invalid}", true},
		{"missing section", "handlers: []", true},
		{"empty section", "cli: {}", true},
		{"empty document", "", false},
		{"multi document", "cli: {}\n---\ncli: {}", false},
		{"unknown", "cli: {private-sentinel: secret-sentinel}", false},
		{"duplicate", "cli: {output: table, output: secret-sentinel}", false},
		{"duplicate section", "cli: {}\ncli: {}", false},
		{"number", "cli: {timeout: 42}", false},
		{"null", "cli: {output: null}", false},
		{"alias", "cli: {output: &a table, color: *a}", false},
		{"section list", "cli: []", false},
		{"malformed", "cli: [secret-sentinel", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.text), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, true)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if err != nil && (strings.Contains(err.Error(), "secret-sentinel") || strings.Contains(err.Error(), "private-sentinel")) {
				t.Fatal("unsafe diagnostic", err)
			}
		})
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := Load(missing, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(missing, true); err == nil {
		t.Fatal("explicit missing file accepted")
	}
	if _, err := Load("", false); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := Load(t.TempDir(), false); err == nil {
		t.Fatal("unreadable file accepted")
	}
}
