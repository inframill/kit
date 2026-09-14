package backend

import (
	"context"
	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

func bindingPointer(info inframill.InstanceInfo) *drivers.Binding {
	value := binding(info)
	return &value
}

// Driver health is cached, so catalog projection performs no executable I/O.
func driverCommands(manager drivers.Manager) []inframill.CommandDefinition {
	entries, err := manager.List(context.Background())
	if err != nil {
		return nil
	}
	var result []inframill.CommandDefinition
	for _, entry := range entries {
		if entry.Status != drivers.Connected || entry.Info == nil {
			continue
		}
		for name, op := range entry.Info.Commands {
			result = append(result, inframill.CommandDefinition{Name: entry.ID + "." + name, Description: op.Description, ArgumentsSchema: op.ArgumentsSchema, Safety: inframill.SafetyClass(op.Safety), Effects: op.Effects, Verification: op.Verification})
		}
	}
	return result
}

// ListDrivers projects cached Manager health without accessing Instance state.
func (b *Backend) ListDrivers(ctx context.Context) ([]inframill.DriverInfo, error) {
	entries, err := b.drivers.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]inframill.DriverInfo, 0, len(entries))
	for _, entry := range entries {
		info := inframill.DriverInfo{ID: entry.ID, Status: inframill.DriverStatus(entry.Status), Reason: entry.Reason, CheckedAt: entry.CheckedAt}
		if entry.Status == drivers.Connected && entry.Info != nil {
			info.Name, info.Version = entry.Info.Name, entry.Info.Version
		}
		result = append(result, info)
	}
	return result, nil
}
