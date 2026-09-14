package inframill_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

type healthBackend struct {
	inframill.Backend
	entries []inframill.DriverInfo
	err     error
	ctx     context.Context
}

func (b *healthBackend) ListDrivers(ctx context.Context) ([]inframill.DriverInfo, error) {
	b.ctx = ctx
	return b.entries, b.err
}

// SPEC-0007 REQ-001 / AC-001: listing needs no Start or other Backend methods.
func TestREQ001ListDriversSnapshots(t *testing.T) {
	stamp := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	b := &healthBackend{entries: []inframill.DriverInfo{
		{ID: "z-offline", Status: inframill.DriverOffline, Reason: "missing executable", CheckedAt: stamp},
		{ID: "a-alias", Name: "reported", Version: "1.2.3", Status: inframill.DriverConnected, CheckedAt: stamp},
	}}
	e, err := inframill.New(t.Context(), &engineSource{}, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	got, err := e.ListDrivers(t.Context())
	want := []inframill.DriverInfo{b.entries[1], b.entries[0]}
	if err != nil || !reflect.DeepEqual(got, want) || b.ctx != t.Context() {
		t.Fatal(got, err)
	}
	got[0].Name = "changed"
	if b.entries[0].ID != "z-offline" || b.entries[1].Name != "reported" {
		t.Fatal("query sorted or modified backend storage", b.entries)
	}
	again, err := e.ListDrivers(t.Context())
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatal(again, err)
	}
	b.entries = nil
	empty, err := e.ListDrivers(t.Context())
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
	b.err = errors.New("catalog unavailable")
	if _, err := e.ListDrivers(t.Context()); !errors.Is(err, b.err) {
		t.Fatal(err)
	}
	b.ctx = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.ListDrivers(ctx); !errors.Is(err, context.Canceled) || b.ctx != nil {
		t.Fatal("cancellation must prevent backend access", err)
	}
}
