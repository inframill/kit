// Package output renders human-facing CLI views without owning domain behavior.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

const (
	Table = "table"
	JSON  = "json"
)

func Validate(format string, log bool) error {
	if format == "" || format == JSON || (format == Table && !log) {
		return nil
	}
	if log {
		return errors.New("log commands support --output json only; omit --output for raw bytes")
	}
	return errors.New("output must be table or json")
}

type Section struct {
	Name string
	JSON json.RawMessage
}

type View struct {
	Headers    []string
	Rows       [][]string
	Empty      string
	Sections   []Section
	NextCursor string
	ObservedAt string
}

func Details(rows ...[]string) View {
	return View{Headers: []string{"Field", "Value"}, Rows: rows}
}

// Write renders first, then checks the write rather than using output mirroring.
func (v View) Write(w io.Writer) error {
	var out strings.Builder
	if len(v.Rows) == 0 {
		out.WriteString(v.Empty + "\n")
	} else {
		t := table.NewWriter()
		t.SetStyle(table.StyleDefault)
		header := make(table.Row, len(v.Headers))
		for i, s := range v.Headers {
			header[i] = tableCell(s)
		}
		t.AppendHeader(header)
		for _, cells := range v.Rows {
			row := make(table.Row, len(cells))
			for i, s := range cells {
				row[i] = tableCell(s)
			}
			t.AppendRow(row)
		}
		out.WriteString(t.Render() + "\n")
	}
	for _, section := range v.Sections {
		if len(section.JSON) == 0 {
			continue
		}
		var data bytes.Buffer
		if err := json.Indent(&data, section.JSON, "", "  "); err != nil {
			return fmt.Errorf("render %s: %w", section.Name, err)
		}
		out.WriteString("\n" + section.Name + ":\n" + data.String() + "\n")
	}
	if v.ObservedAt != "" {
		out.WriteString("Observed at: " + v.ObservedAt + "\n")
	}
	if v.NextCursor != "" {
		out.WriteString("Next cursor: " + v.NextCursor + "\n")
	}
	data := out.String()
	n, err := io.WriteString(w, data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

// tableCell limits visible width without changing the source or JSON output.
func tableCell(value string) string {
	value = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", "    ").Replace(value)
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = text.Snip(line, 20, "...")
	}
	return strings.Join(lines, "\n")
}
