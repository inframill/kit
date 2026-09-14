package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

// Namespace mutations share this lock across Backend objects and processes.
func (b *Backend) lockNamespace(ctx context.Context) (*flock.Flock, error) {
	if err := directory(b.root); err != nil {
		return nil, err
	}
	path := filepath.Join(b.root, ".initialization.lock")
	if err := regularFile(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	lock := flock.New(path)
	locked, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil || !locked {
		_ = lock.Close()
		if err != nil {
			return nil, err
		}
		return nil, inframill.ErrConflict
	}
	return lock, nil
}

func sameDirectory(path string, original os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if original == nil || !current.IsDir() || !os.SameFile(current, original) {
		return errors.New("Instance directory replaced; existing data preserved")
	}
	return nil
}

// Must be called with i.mu held once the Instance is visible to callers.
// Unsupported identity is never repaired by a best-effort failure write.
func (i *instance) checkIdentity() error {
	if !i.owned || !inframill.ValidUUID(string(i.info.ID)) || i.root != filepath.Join(i.backend.root, string(i.info.ID)) {
		return errors.New("Instance workspace is not owned")
	}
	if err := sameDirectory(i.backend.root, i.parentIdentity); err != nil {
		return err
	}
	if err := sameDirectory(i.root, i.rootIdentity); err != nil {
		return err
	}
	info, err := readInfo(i.root, i.info.ID)
	if info.ID == "" {
		return stateError("verify Instance identity", err)
	}
	if info.Name != i.info.Name {
		return errors.New("Instance name changed; existing data preserved")
	}
	return nil
}
