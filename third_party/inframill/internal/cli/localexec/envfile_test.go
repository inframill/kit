package localexec

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// SPEC-0000 REQ-020: dotenv syntax is data, with no interpolation or execution.
func TestEnvFileSyntax(t *testing.T) {
	data := "# comment\r\n\r\n export TOKEN = first\r\nTOKEN=last # comment\nEMPTY=\nCOMMENT= # empty\n" +
		"HASH=abc#def\nSINGLE=' spaced # $TOKEN \\n ' # comment\n" +
		"DOUBLE=\"a\\n\\r\\t\\\\\\\"\\$TOKEN\\q\"\n" +
		"LITERAL=$(touch sentinel); `touch sentinel` ${TOKEN}\nUNICODE=Grüße\nexport\tTABBED=yes\n"
	got, err := parseEnvFile(data)
	want := map[string]string{
		"TOKEN": "last", "EMPTY": "", "COMMENT": "", "HASH": "abc#def",
		"SINGLE": " spaced # $TOKEN \\n ", "DOUBLE": "a\n\r\t\\\"$TOKEN\\q",
		"LITERAL": "$(touch sentinel); `touch sentinel` ${TOKEN}", "UNICODE": "Grüße", "TABBED": "yes",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("dotenv syntax mismatch", got, err)
	}
	if got, err := parseEnvFile(""); err != nil || len(got) != 0 {
		t.Fatal("empty dotenv file rejected", err)
	}
}

// SPEC-0000 REQ-020: errors identify lines without including contents.
func TestEnvFileMalformed(t *testing.T) {
	for _, line := range []string{
		"sensitive-marker", "1BAD=sensitive-marker", "BAD-KEY=sensitive-marker", "=sensitive-marker",
		"KEY='sensitive-marker", `KEY="sensitive-marker`, `KEY="sensitive-marker"junk`,
		`KEY='sensitive-marker'#comment`, "KEY=sensitive-marker\x00", "KEY=sensitive-marker\xff",
	} {
		_, err := parseEnvFile("# first line\n" + line)
		var failure *ExitError
		if !errors.As(err, &failure) || failure.Code != 2 || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "sensitive-marker") {
			t.Fatal("unsafe or missing parse error", err)
		}
	}
}

// SPEC-0000 REQ-020: inherit even empty values without mutating either input.
func TestEnvFileSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(path, []byte("TOKEN=file\nEMPTY=file\nFILE_ONLY=literal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const (
		empty      = "EMPTY="
		shellToken = "TOKEN=shell"
	)
	inherited := []string{shellToken, empty}
	before := os.Environ()
	got, err := loadEnvFile(options{envFile: path}, inherited)
	if err != nil || !reflect.DeepEqual(got, []string{empty, "FILE_ONLY=literal", shellToken}) {
		t.Fatal(got, err)
	}
	if !reflect.DeepEqual(before, os.Environ()) {
		t.Fatal("process environment mutated")
	}
	if !reflect.DeepEqual(inherited, []string{shellToken, empty}) {
		t.Fatal("inherited snapshot mutated")
	}
}

// SPEC-0000 REQ-020: containment resolves parent aliases and missing directories.
func TestEnvFileLocation(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, o := range []options{
		{envFile: filepath.Join(root, "file.env"), blueprint: root},
		{envFile: filepath.Join(alias, "file.env"), blueprint: root},
		{envFile: filepath.Join(root, "file.env"), blueprint: alias},
		{envFile: filepath.Join(alias, "missing", "file.env"), workspace: filepath.Join(root, "missing")},
	} {
		if err := checkEnvFileLocation(o); err == nil {
			t.Fatal("allowed environment input in persisted directory")
		}
	}
	if err := checkEnvFileLocation(options{envFile: filepath.Join(root, "source-extra", "file.env"), blueprint: filepath.Join(root, "source")}); err != nil {
		t.Fatal("rejected sibling directory", err)
	}
}
