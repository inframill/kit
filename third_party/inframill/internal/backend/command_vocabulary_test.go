package backend

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

// SPEC-0011 REQ-004: supported records reopen; old/mixed request fields reject.
func TestREQ004CommandRecordVocabulary(t *testing.T) {
	record := inframill.CommandExecution{Request: request(10, "record"), Status: inframill.Queued, CreatedAt: time.Now().UTC()}
	data, err := encodeCommand(record)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"command":"test.run"`)) || bytes.Contains(data, []byte(`"operation"`)) {
		t.Fatal(string(data))
	}
	if decoded, err := decodeCommand(data); err != nil || decoded.Request.Command != record.Request.Command {
		t.Fatal(decoded, err)
	}
	for _, invalid := range [][]byte{
		bytes.Replace(data, []byte(`"command":`), []byte(`"operation":`), 1),
		bytes.Replace(data, []byte(`"command":`), []byte(`"operation":"private-sentinel","command":`), 1),
		append(bytes.Clone(data), []byte(` {}`)...),
	} {
		if _, err := decodeCommand(invalid); err == nil || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal("legacy/mixed record accepted or leaked", err)
		}
	}
}

// SPEC-0012 REQ-002: invalid versions and current-schema records preserve all files.
func TestREQ002InvalidCommandStoresPreserveInstance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		status  inframill.CommandStatus
		field   string
	}{
		{name: "version2", version: 2, status: inframill.Queued},
		{name: "version99", version: 99, status: inframill.Queued},
		{name: "unknown_field_queued", version: 1, status: inframill.Queued, field: "operation"},
		{name: "unknown_field_terminal", version: 1, status: inframill.Succeeded, field: "operation"},
		{name: "missing_command", version: 1, status: inframill.Succeeded, field: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, i := fixture(t, 1, nil)
			if err := i.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			store, err := openStore(t.Context(), i.root, false)
			if err != nil {
				t.Fatal(err)
			}
			var version int
			if err := store.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 1 {
				t.Fatal("new store must use version 1", version, err)
			}
			record := inframill.CommandExecution{Request: request(10, "record"), Status: tc.status, CreatedAt: time.Now().UTC()}
			if err := store.insert(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			if tc.version == 1 {
				data, err := encodeCommand(record)
				if err != nil {
					t.Fatal(err)
				}
				if tc.field == "" {
					data = bytes.Replace(data, []byte(`"command":"test.run",`), nil, 1)
				} else {
					data = bytes.Replace(data, []byte(`"command":`), []byte(`"`+tc.field+`":`), 1)
				}
				if _, err := store.db.ExecContext(t.Context(), "UPDATE commands SET record=?", data); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.db.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version=%d", tc.version)); err != nil {
				t.Fatal(err)
			}
			if err := store.db.Close(); err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			if err := filepath.WalkDir(i.root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				data, err := os.ReadFile(path)
				before[path] = data
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := openStore(t.Context(), i.root, false); err == nil {
				t.Fatal("invalid store opened")
			}
			restored, err := b.SpawnInstance(t.Context(), i.Info().ID)
			if err != nil || restored.Info().Status != inframill.InstanceFailed || !strings.Contains(restored.Info().Failure.Message, "existing data preserved") {
				t.Fatal(restored, err)
			}
			if _, err := restored.SubmitCommand(t.Context(), request(11, "rejected")); err == nil {
				t.Fatal("invalid store admitted command")
			}
			if err := restored.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			count := 0
			if err := filepath.WalkDir(i.root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				count++
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				saved, exists := before[path]
				if !exists || !bytes.Equal(data, saved) {
					t.Errorf("Instance file changed: %s", filepath.Base(path))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if count != len(before) {
				t.Fatal("Instance files removed")
			}
		})
	}
}
