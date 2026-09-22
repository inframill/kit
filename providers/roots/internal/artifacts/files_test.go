package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBoundedPrivateFilesAndSourceIdentity(t *testing.T) {
	dir := t.TempDir()
	if e := WriteBytes(dir, "settings.json", []byte(`{}`)); e != nil {
		t.Fatal(e)
	}
	st, _ := os.Stat(filepath.Join(dir, "settings.json"))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	first, e := SourceHash(dir)
	if e != nil {
		t.Fatal(e)
	}
	WriteBytes(dir, "settings.json", []byte(`{"changed":true}`))
	second, _ := SourceHash(dir)
	if first == second {
		t.Fatal("source change missed")
	}
	for _, name := range []string{"../outside", "a/b", "/absolute"} {
		if WriteBytes(dir, name, nil) == nil {
			t.Fatal("unsafe write accepted", name)
		}
	}
	os.Symlink(filepath.Join(dir, "settings.json"), filepath.Join(dir, "link"))
	if _, e = Read(dir, "link", 100); e == nil {
		t.Fatal("link accepted")
	}
	if _, e = Read(dir, "settings.json", 1); e == nil {
		t.Fatal("oversized read accepted")
	}
	if Decode([]byte(`{"secret":"private"}`), &struct{}{}) == nil {
		t.Fatal("unknown field accepted")
	}
}
