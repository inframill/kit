package connectadapter_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/sir-hassan/inframill/internal/blueprint"
	"github.com/sir-hassan/inframill/internal/connectadapter"
	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

type healthBackend struct {
	inframill.Backend
	entries []inframill.DriverInfo
	err     error
}

func (b healthBackend) ListDrivers(context.Context) ([]inframill.DriverInfo, error) {
	return b.entries, b.err
}

// SPEC-0007 REQ-002 / AC-002: the generated client sees the same query,
// including cached timestamps, offline results and typed error mapping.
func TestREQ002ListDriversRPC(t *testing.T) {
	stamp := time.Date(2026, 9, 13, 12, 0, 0, 123000000, time.UTC)
	for _, tc := range []struct {
		name    string
		backend healthBackend
		code    connect.Code
	}{
		{name: "health", backend: healthBackend{entries: []inframill.DriverInfo{
			{ID: "missing", Status: inframill.DriverOffline, Reason: "not executable", CheckedAt: stamp},
			{ID: "alias", Name: "debug", Version: "1.2.3", Status: inframill.DriverConnected, CheckedAt: stamp},
		}}},
		{name: "empty"},
		{name: "failure", backend: healthBackend{err: errors.New("catalog failure")}, code: connect.CodeInternal},
		{name: "canceled", backend: healthBackend{err: context.Canceled}, code: connect.CodeCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := blueprint.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			e, err := inframill.New(t.Context(), source, tc.backend)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(e.Close)
			server := httptest.NewServer(connectadapter.Handler(e))
			defer server.Close()
			client := inframillv1connect.NewEngineServiceClient(server.Client(), server.URL)
			for range 2 {
				got, err := client.ListDrivers(t.Context(), connect.NewRequest(&pb.ListDriversRequest{}))
				if tc.backend.err != nil {
					if connect.CodeOf(err) != tc.code {
						t.Fatal(err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if got.Header().Get("Cache-Control") != "no-store" || len(got.Msg.GetDrivers()) != len(tc.backend.entries) {
					t.Fatal(got)
				}
				if len(tc.backend.entries) == 0 {
					continue
				}
				healthy, offline := got.Msg.Drivers[0], got.Msg.Drivers[1]
				if healthy.Id != "alias" || healthy.Name != "debug" || healthy.Version != "1.2.3" || healthy.Status != pb.DriverStatus_DRIVER_STATUS_CONNECTED || healthy.Reason != "" || !healthy.CheckedAt.AsTime().Equal(stamp) {
					t.Fatal(healthy)
				}
				if offline.Id != "missing" || offline.Name != "" || offline.Version != "" || offline.Status != pb.DriverStatus_DRIVER_STATUS_OFFLINE || offline.Reason != "not executable" || !offline.CheckedAt.AsTime().Equal(stamp) {
					t.Fatal(offline)
				}
			}
		})
	}
}
