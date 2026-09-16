// Package artifacts owns bounded, rooted Kit files and cross-driver node records.
package artifacts

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

const Limit = 32 << 20
const SettingsLimit = 1 << 20

func Decode(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	d.UseNumber()
	if d.Decode(target) != nil {
		return errors.New("invalid JSON fields or types")
	}
	if !errors.Is(d.Decode(new(any)), io.EOF) {
		return errors.New("expected one JSON document")
	}
	return nil
}
func Hash(data []byte) string   { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func JSONHash(value any) string { data, _ := json.Marshal(value); return Hash(data) }
func Relative(name string) bool {
	return name != "." && filepath.IsLocal(name) && filepath.Clean(name) == name && !strings.ContainsAny(name, "\\\x00")
}
func Flat(name string) bool { return Relative(name) && filepath.Base(name) == name }

func Read(directory, name string, limit int64) ([]byte, error) {
	if !Relative(name) {
		return nil, errors.New("invalid relative file path")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for path := name; path != "."; path = filepath.Dir(path) {
		info, e := root.Lstat(path)
		if e != nil {
			return nil, e
		}
		if info.Mode()&os.ModeSymlink != 0 || path != name && !info.IsDir() {
			return nil, errors.New("file path contains a link or non-directory")
		}
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("nonregular or oversized file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if len(data) > int(limit) {
		return nil, errors.New("file exceeds size limit")
	}
	return data, err
}
func Write(directory, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return WriteBytes(directory, name, data)
}
func WriteBytes(directory, name string, data []byte) error {
	if !Flat(name) || len(data) > Limit {
		return errors.New("invalid artifact name or size")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if info, e := root.Lstat(name); e == nil && !info.Mode().IsRegular() {
		return errors.New("nonregular destination")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	// Create through the opened root so publication never escapes an exchanged path.
	temp, err := randomName()
	if err != nil {
		return err
	}
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, writeErr := f.Write(data)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err = root.Rename(temp, name); err != nil {
		return err
	}
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func Remove(directory, name string) error {
	if !Flat(name) {
		return errors.New("invalid artifact name")
	}
	r, e := os.OpenRoot(directory)
	if e != nil {
		return e
	}
	defer r.Close()
	return r.Remove(name)
}
func SourceHash(directory string) (string, error) {
	var paths []string
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("source links are forbidden")
		}
		if entry.IsDir() {
			if entry.Name() == ".terraform" || strings.HasPrefix(entry.Name(), ".kit-") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(entry.Name(), ".kit-") && !strings.Contains(entry.Name(), ".tfstate") {
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		data, err := Read(directory, path, Limit)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%s\x00", filepath.ToSlash(path), Hash(data))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
