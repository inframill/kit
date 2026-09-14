// Package content loads Blueprint source bytes without interpreting templates.
package content

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sir-hassan/inframill/internal/platform/filetree"
)

// Snapshot loads source excluding root runtime data, rejecting other links and special files.
func Snapshot(root string) (map[string]filetree.File, error) {
	files := map[string]filetree.File{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "runtime" || relative == ".inframill" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported Blueprint entry %s", relative)
		}
		file := filetree.File{Directory: info.IsDir(), Executable: info.Mode().Perm()&0o111 != 0}
		if !file.Directory {
			file.Content, err = os.ReadFile(name) //nolint:gosec // Regular entry in the explicitly selected Blueprint tree.
			if err != nil {
				return err
			}
		}
		files[relative] = file
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
