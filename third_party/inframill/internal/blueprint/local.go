package blueprint

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/sir-hassan/inframill/internal/blueprint/content"
)

// LocalSource owns a private, immutable snapshot of a local Blueprint.
// Resolve it using the name "blueprint". Close removes only this temporary copy.
type LocalSource struct {
	*Source
	temporary string
}

func SnapshotLocal(ctx context.Context, directory string) (*LocalSource, error) {
	if _, err := metadata(directory); err != nil {
		return nil, err
	}
	files, err := content.Snapshot(directory)
	if err != nil {
		return nil, err
	}
	archive, err := Pack(files)
	if err != nil {
		return nil, err
	}
	if _, unpackErr := unpack(ctx, archive, descriptor("", archive.Manifest).Digest.String()); unpackErr != nil {
		return nil, unpackErr
	}
	temporary, err := os.MkdirTemp("", "inframill-local-blueprint-")
	if err != nil {
		return nil, err
	}
	source := &LocalSource{Source: &Source{root: temporary}, temporary: temporary}
	root := filepath.Join(temporary, "blueprint")
	write := func() error {
		if err := os.Mkdir(root, 0o700); err != nil {
			return err
		}
		for _, name := range sortedKeys(files) {
			if err := ctx.Err(); err != nil {
				return err
			}
			file := files[name]
			path := filepath.Join(root, filepath.FromSlash(name))
			if file.Directory {
				if err := os.MkdirAll(path, 0o700); err != nil {
					return err
				}
				continue
			}
			mode := os.FileMode(0o600)
			if file.Executable {
				mode = 0o700
			}
			if err := os.WriteFile(path, file.Content, mode); err != nil {
				return err
			}
		}
		_, err := metadata(root)
		return err
	}
	if err := write(); err != nil {
		return nil, errors.Join(err, source.Close())
	}
	return source, nil
}

func (s *LocalSource) Close() error { return os.RemoveAll(s.temporary) }
