package localexec

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sir-hassan/inframill/internal/appconfig"
	"github.com/sir-hassan/inframill/internal/backend"
	"github.com/sir-hassan/inframill/internal/blueprint"
	clioutput "github.com/sir-hassan/inframill/internal/cli/output"
	"github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

type DriverListOptions struct {
	Config, Output, Columns string
	ColumnsSet              bool
}

type driverSummary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason"`
	CheckedAt time.Time `json:"checkedAt"`
}

// ListDrivers composes the same Engine and Backend as exec, without starting
// Instance workers or touching storage. Only Manager construction probes Drivers.
func ListDrivers(ctx context.Context, input DriverListOptions, environ []string, stdout io.Writer) error {
	columns, err := driverColumns(input)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := appconfig.Load(ctx, input.Config, environ)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalid(err.Error())
	}
	manager, err := driver.New(ctx, config.Drivers, environ)
	if err != nil {
		return err
	}
	b, err := backend.New(filepath.Join(config.DataDir, "instances"), manager, backend.Options{})
	if err != nil {
		return err
	}
	source, err := blueprint.New(filepath.Join(config.DataDir, "blueprints"))
	if err != nil {
		return err
	}
	e, err := inframill.New(ctx, source, b)
	if err != nil {
		return err
	}
	defer e.Close()
	entries, err := e.ListDrivers(ctx)
	if err != nil {
		return err
	}
	return writeDrivers(stdout, input.Output, columns, entries)
}

var driverColumnKeys = []string{"id", "name", "version", "status", "reason", "checked_at"}

func driverColumns(input DriverListOptions) ([]int, error) {
	if err := clioutput.Validate(input.Output, false); err != nil {
		return nil, invalid(err.Error())
	}
	if !input.ColumnsSet {
		return []int{0, 1, 2, 3, 4, 5}, nil
	}
	if input.Output == clioutput.JSON {
		return nil, invalid("columns are only supported for table output")
	}
	var columns []int
	for key := range strings.SplitSeq(input.Columns, ",") {
		index := slices.Index(driverColumnKeys, key)
		if index < 0 || slices.Contains(columns, index) {
			return nil, invalid("columns must be known, nonempty and unique")
		}
		columns = append(columns, index)
	}
	return columns, nil
}

func writeDrivers(w io.Writer, format string, columns []int, entries []inframill.DriverInfo) error {
	if format == clioutput.JSON {
		result := struct {
			Drivers []driverSummary `json:"drivers"`
		}{Drivers: make([]driverSummary, 0, len(entries))}
		for _, entry := range entries {
			result.Drivers = append(result.Drivers, driverSummary{ID: entry.ID, Name: entry.Name, Version: entry.Version, Status: string(entry.Status), Reason: entry.Reason, CheckedAt: entry.CheckedAt})
		}
		return json.NewEncoder(w).Encode(result)
	}
	headers := []string{"ID", "Name", "Version", "Status", "Reason", "Checked at"}
	view := clioutput.View{Empty: "No drivers configured."}
	for _, column := range columns {
		view.Headers = append(view.Headers, headers[column])
	}
	for _, entry := range entries {
		cells := []string{entry.ID, entry.Name, entry.Version, string(entry.Status), entry.Reason, historyTime(&entry.CheckedAt)}
		row := make([]string, 0, len(columns))
		for _, column := range columns {
			row = append(row, cells[column])
		}
		view.Rows = append(view.Rows, row)
	}
	return view.Write(w)
}
