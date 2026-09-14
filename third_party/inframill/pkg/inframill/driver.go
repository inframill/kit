package inframill

import (
	"context"
	"sort"
	"time"
)

type DriverStatus string

const (
	DriverConnected DriverStatus = "connected"
	DriverOffline   DriverStatus = "offline"
)

// DriverInfo summarizes the last info probe. ID is the configured identity;
// Name and Version are reported by the executable and are empty when offline.
// Connected does not describe infrastructure health or a persistent connection.
type DriverInfo struct {
	ID        string
	Name      string
	Version   string
	Status    DriverStatus
	Reason    string
	CheckedAt time.Time
}

// ListDrivers returns sorted, independent cached health snapshots. It is a
// read-only query available before Start; it never starts Instance workers.
func (e *Engine) ListDrivers(ctx context.Context) ([]DriverInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := e.backend.ListDrivers(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := append([]DriverInfo{}, entries...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
