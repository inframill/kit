package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

type storedInstance struct {
	root string
	info inframill.InstanceInfo
}

// Discovery validates the entire namespace before publishing any identities.
func unsupportedLayout() error {
	return errors.New("unsupported Instance storage layout; existing data preserved; use a new empty dataDir")
}

func (b *Backend) discover(ctx context.Context) ([]storedInstance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := directory(b.root); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(b.root)
	if err != nil {
		return nil, err
	}
	var found []storedInstance
	seenNames := make(map[string]bool)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		} // Coordination and unpublished staging.
		if !entry.IsDir() || !inframill.ValidUUID(entry.Name()) {
			return nil, unsupportedLayout()
		}
		root := filepath.Join(b.root, entry.Name())
		if err := directory(root); err != nil {
			return nil, err
		}
		info, readErr := readInfo(root, inframill.InstanceID(entry.Name()))
		if info.ID == "" {
			return nil, stateError("read Instance identity", readErr)
		}
		if seenNames[info.Name] {
			return nil, errors.New("duplicate Instance name in storage; existing data preserved")
		}
		seenNames[info.Name] = true
		// Valid identities with broken execution metadata remain isolated failed Instances.
		found = append(found, storedInstance{root: root, info: info})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].info.ID < found[j].info.ID })
	return found, nil
}

func (b *Backend) GetAllIDs(ctx context.Context) ([]inframill.InstanceID, error) {
	stored, err := b.discover(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]inframill.InstanceID, 0, len(stored))
	for _, item := range stored {
		ids = append(ids, item.info.ID)
	}
	return ids, nil
}

func (b *Backend) instancePath(ctx context.Context, id inframill.InstanceID) (string, error) {
	stored, err := b.discover(ctx)
	if err != nil {
		return "", err
	}
	for _, item := range stored {
		if item.info.ID == id {
			return item.root, nil
		}
	}
	return "", fmt.Errorf("resolve Instance directory: %w", inframill.ErrNotFound)
}
