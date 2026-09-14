package driverkit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxArtifact = 32 << 20

func decode(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("expected exactly one JSON document")
	}
	return nil
}
func hash(data []byte) string { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }
func jsonHash(v any) string   { b, _ := json.Marshal(v); return hash(b) }
func safeRelative(name string) bool {
	return name != "." && filepath.IsLocal(name) && !strings.ContainsAny(name, "\\\x00") && filepath.Clean(name) == name
}

// Root confines all reads and writes even if a directory is exchanged concurrently.
func readPrivate(dir, name string, limit int64) ([]byte, error) {
	if !safeRelative(name) {
		return nil, errors.New("unsafe artifact path")
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	for p := filepath.Dir(name); p != "."; p = filepath.Dir(p) {
		st, e := r.Lstat(p)
		if e != nil {
			return nil, e
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("artifact parent is not a real directory")
		}
	}
	st, e := r.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid or oversized artifact")
	}
	f, e := r.OpenFile(name, os.O_RDONLY, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if len(b) > int(limit) {
		return nil, errors.New("artifact exceeds limit")
	}
	return b, e
}
func writePrivate(dir, name string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return writeBytes(dir, name, b)
}
func writeBytes(dir, name string, b []byte) error {
	if !safeRelative(name) || len(b) > maxArtifact {
		return errors.New("unsafe or oversized artifact")
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return e
	}
	defer r.Close()
	if st, e := r.Lstat(name); e == nil && !st.Mode().IsRegular() {
		return errors.New("artifact destination is not regular")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	// Artifact names are flat; do not follow or create caller-selected parent directories.
	if filepath.Base(name) != name {
		return errors.New("artifact name must be flat")
	}
	f, e := os.CreateTemp(dir, ".kit-write-")
	if e != nil {
		return e
	}
	tmp := filepath.Base(f.Name())
	defer r.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return r.Rename(tmp, name)
}
func sourceHash(dir string) (string, error) {
	var names []string
	e := filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source link rejected: %s", rel)
		}
		if d.IsDir() {
			if d.Name() == ".terraform" || strings.HasPrefix(d.Name(), ".kit-") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.Contains(d.Name(), ".tfstate") && !strings.HasPrefix(d.Name(), ".kit-") {
			names = append(names, rel)
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, e := readPrivate(dir, n, maxArtifact)
		if e != nil {
			return "", e
		}
		fmt.Fprintf(h, "%s\x00%s\x00", n, hash(b))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
