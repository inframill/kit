package debugdriver

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type directoryArtifacts struct{ directory string }

func (files directoryArtifacts) open(name string) (*os.Root, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return nil, errors.New("artifact name must be a single filename")
	}
	info, err := os.Lstat(files.directory)
	if err != nil || !info.IsDir() {
		return nil, errors.New("invalid artifact directory")
	}
	root, err := os.OpenRoot(files.directory)
	if err != nil {
		return nil, err
	}
	info, err = root.Lstat(name)
	if err != nil && !os.IsNotExist(err) {
		return nil, errors.Join(err, root.Close())
	}
	if err == nil && !info.Mode().IsRegular() {
		return nil, errors.Join(errors.New("artifact must be a regular file"), root.Close())
	}
	return root, nil
}

func (files directoryArtifacts) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := files.open(name)
	if err != nil {
		return nil, err
	}
	defer root.Close() //nolint:errcheck // Read-only directory handle.
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // Read-only artifact handle.
	data, err := io.ReadAll(io.LimitReader(file, (64<<20)+1))
	if len(data) > 64<<20 {
		return nil, errors.New("artifact exceeds size limit")
	}
	return data, err
}

func (files directoryArtifacts) WriteFile(ctx context.Context, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) > 64<<20 {
		return errors.New("artifact exceeds size limit")
	}
	root, err := files.open(name)
	if err != nil {
		return err
	}
	defer root.Close() //nolint:errcheck // Publication is synced through its file and directory.

	file, err := os.CreateTemp(files.directory, ".driver-artifact-")
	if err != nil {
		return err
	}
	temporary := filepath.Base(file.Name())
	defer root.Remove(temporary) //nolint:errcheck // Absent after successful publication.
	_, err = file.Write(data)
	if err = errors.Join(err, file.Sync(), file.Close()); err != nil {
		return err
	}
	if err = root.Rename(temporary, name); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
