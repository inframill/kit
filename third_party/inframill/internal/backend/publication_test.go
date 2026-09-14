package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

type publicationDrivers struct {
	drivers.Manager
	afterValidate func() error
}

func (d publicationDrivers) Validate(ctx context.Context, r drivers.ValidationRequest) error {
	if err := d.Manager.Validate(ctx, r); err != nil {
		return err
	}
	return d.afterValidate()
}

// SPEC-0009 REQ-004: cancellation and late destination collisions remove only
// this initializer's staging tree. A published sibling is never overwritten.
func TestREQ004PublicationFailuresPreserveData(t *testing.T) {
	for _, kind := range []string{"cancel", "directory", "symlink", "parent-replaced"} {
		t.Run(kind, func(t *testing.T) {
			b := namedBackend(t)
			initNamed(t, b, 1, "project/sibling")
			sibling := filepath.Join(b.root, string(testID(1)))
			before := storageSnapshot(t, sibling)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			target := filepath.Join(b.root, string(testID(2)))
			external := t.TempDir()
			b.drivers = publicationDrivers{Manager: b.drivers, afterValidate: func() error {
				switch kind {
				case "cancel":
					cancel()
					return nil
				case "symlink":
					return os.Symlink(external, target)
				case "parent-replaced":
					backup := filepath.Join(external, "original")
					if err := os.Rename(b.root, backup); err != nil {
						return err
					}
					sibling = filepath.Join(backup, string(testID(1)))
					return os.Mkdir(b.root, 0700)
				default:
					if err := os.Mkdir(target, 0700); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join(target, "sentinel"), []byte("late destination"), 0600)
				}
			}}
			err := b.InitInstance(ctx, testID(2), "project/new", testBlueprint(t), inframill.InstanceValues{messageKey: helloMessage})
			if err == nil {
				t.Fatal("publication unexpectedly succeeded")
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if after := storageSnapshot(t, sibling); !reflect.DeepEqual(before, after) {
				t.Fatal("sibling changed")
			}
			if kind == "directory" {
				if data, err := os.ReadFile(filepath.Join(target, "sentinel")); err != nil || string(data) != "late destination" {
					t.Fatal("destination overwritten", err)
				}
			}
			if kind == "symlink" {
				if link, err := os.Readlink(target); err != nil || link != external {
					t.Fatal("link overwritten", err)
				}
				if entries, err := os.ReadDir(external); err != nil || len(entries) != 0 {
					t.Fatal("escaped namespace", err)
				}
			}
			for _, root := range []string{b.root, filepath.Dir(sibling)} {
				staging, err := filepath.Glob(filepath.Join(root, ".init-*"))
				if err != nil || len(staging) != 0 {
					t.Fatal("unpublished staging survived", staging, err)
				}
			}
		})
	}
}
