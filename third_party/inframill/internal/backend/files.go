package backend

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func regularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", filepath.Base(path))
	}
	return nil
}

func directory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid workspace directory")
	}
	return nil
}

func readRegular(root, name string, limit int64) ([]byte, error) {
	if err := regularFile(filepath.Join(root, name)); err != nil {
		return nil, err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close() //nolint:errcheck // Read-only directory handle.
	f, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // Read-only file handle.
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("file exceeds size limit")
	}
	return b, err
}

func writeAtomic(path string, content []byte) error {
	if err := regularFile(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if err := directory(parent); err != nil {
		return err
	}
	f, checkErr := os.CreateTemp(parent, ".write-")
	if checkErr != nil {
		return checkErr
	}
	defer os.Remove(f.Name()) //nolint:errcheck // Staging file is absent after successful rename.
	if err := f.Chmod(0o600); err != nil {
		return errors.Join(err, f.Close())
	}
	_, writeErr := f.Write(content)
	checkErr = errors.Join(writeErr, f.Sync(), f.Close())
	if checkErr != nil {
		return checkErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	//nolint:gosec // Validated destination directory; open only to sync publication.
	dir, checkErr := os.Open(parent)
	if checkErr != nil {
		return checkErr
	}
	return errors.Join(dir.Sync(), dir.Close())
}

type snapshotReader struct {
	*io.SectionReader
	file *os.File
}

func (s snapshotReader) Close() error { return s.file.Close() }
