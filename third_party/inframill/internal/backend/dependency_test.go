package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

type rejectingDrivers struct{ validated bool }

func (*rejectingDrivers) List(context.Context) ([]drivers.Entry, error) { return nil, nil }
func (d *rejectingDrivers) Validate(context.Context, drivers.ValidationRequest) error {
	d.validated = true
	return errors.New("injected config rejection")
}

func (*rejectingDrivers) Prepare(context.Context, drivers.CommandRequest) (drivers.PreparedCommand, error) {
	return nil, errors.New("unexpected preparation")
}

func TestBackendUsesInjectedDrivers(t *testing.T) {
	d := &rejectingDrivers{}
	b, err := New(t.TempDir(), d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = b.InitInstance(t.Context(), testID(1), "project/dev", testBlueprint(t), inframill.InstanceValues{messageKey: helloMessage})
	if !d.validated || err == nil || err.Error() != "injected config rejection" {
		t.Fatal(d.validated, err)
	}
	if _, err := os.Stat(filepath.Join(b.root, string(testID(1)))); !os.IsNotExist(err) {
		t.Fatal("rejected workspace was published", err)
	}
	var typedNil *rejectingDrivers
	for _, dependency := range []drivers.Manager{nil, typedNil} {
		root := filepath.Join(t.TempDir(), "absent")
		if _, err := New(root, dependency, Options{}); err == nil {
			t.Fatal("nil dependency accepted")
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("nil dependency created workspace", err)
		}
	}
}

// SPEC-0006 REQ-004: offline and unknown Blueprint IDs cannot publish an Instance.
func TestREQ004UnavailableDriverRejectsInitialization(t *testing.T) {
	for _, configured := range []drivers.Configurations{nil, {"test": {Path: filepath.Join(t.TempDir(), "missing")}}} {
		manager, err := drivers.New(t.Context(), configured, nil)
		if err != nil {
			t.Fatal(err)
		}
		b, err := New(t.TempDir(), manager, Options{})
		if err != nil {
			t.Fatal(err)
		}
		err = b.InitInstance(t.Context(), testID(1), "project/dev", testBlueprint(t), inframill.InstanceValues{messageKey: helloMessage})
		if err == nil || !strings.Contains(err.Error(), "drivers.test") {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(b.root, string(testID(1)))); !os.IsNotExist(err) {
			t.Fatal("unavailable Driver published Instance", err)
		}
	}
}
