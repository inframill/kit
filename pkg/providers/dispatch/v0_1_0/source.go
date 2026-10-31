package dispatch

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/inframill/kit/internal/artifacts"
)

// Validate the complete directory before SCP, which would otherwise follow links.
func validateSource(ctx context.Context, workspace, name string) error {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	for parent := name; parent != "."; parent = filepath.Dir(parent) {
		info, err := root.Lstat(parent)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("source path must contain only directories, without links")
		}
	}
	var total int64
	entries := 0
	return fs.WalkDir(root.FS(), name, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("source contains a link or nonregular file")
		}
		entries++
		if !info.IsDir() {
			total += info.Size()
		}
		if entries > 10000 || total > 256<<20 || !info.IsDir() && info.Size() > artifacts.Limit {
			return errors.New("source exceeds entry, total byte or per-file limits")
		}
		return nil
	})
}
