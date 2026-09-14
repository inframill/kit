package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

type healthManager struct {
	drivers.Manager
	entries []drivers.Entry
	err     error
	ctx     context.Context
}

func (m *healthManager) List(ctx context.Context) ([]drivers.Entry, error) {
	m.ctx = ctx
	return m.entries, m.err
}

// SPEC-0007 REQ-001 / AC-001: projection only reads List; a data root that is
// a regular file proves no storage is opened, discovered or initialized.
func TestREQ001DriverHealthProjection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	m := &healthManager{entries: []drivers.Entry{
		{ID: "alias", Status: drivers.Connected, Info: &drivers.Info{Name: "reported", Version: "1.2.3"}, CheckedAt: stamp},
		{ID: "missing", Status: drivers.Offline, Reason: "missing executable", CheckedAt: stamp},
	}}
	b, err := New(root, m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.ListDrivers(t.Context())
	want := []inframill.DriverInfo{
		{ID: "alias", Name: "reported", Version: "1.2.3", Status: inframill.DriverConnected, CheckedAt: stamp},
		{ID: "missing", Status: inframill.DriverOffline, Reason: "missing executable", CheckedAt: stamp},
	}
	if err != nil || !reflect.DeepEqual(got, want) || m.ctx != t.Context() {
		t.Fatal(got, err)
	}
	got[0].Name = "changed"
	again, err := b.ListDrivers(t.Context())
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatal(again, err)
	}
	m.entries = nil
	empty, err := b.ListDrivers(t.Context())
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
	m.err = context.Canceled
	if _, err := b.ListDrivers(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(root)
	if err != nil || string(data) != "preserve" {
		t.Fatal("storage changed", string(data), err)
	}
}
