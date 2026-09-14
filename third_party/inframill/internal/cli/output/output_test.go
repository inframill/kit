package output

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jedib0t/go-pretty/v6/text"
)

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

// SPEC-0000 REQ-015: short writes cannot be mistaken for successful output.
func TestShortWriteAndInvalidSection(t *testing.T) {
	v := Details([]string{"ID", "complete-id"})
	if err := v.Write(shortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	v.Sections = []Section{{Name: "Result", JSON: []byte("invalid")}}
	var out bytes.Buffer
	if err := v.Write(&out); err == nil || out.Len() != 0 {
		t.Fatal("invalid result produced partial output", out.String(), err)
	}
}

// SPEC-0008 REQ-001 / AC-001: the marker fits inside the 20-column limit.
func TestREQ001TableCellWidth(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{"short", "short"},
		{"12345678901234567890", "12345678901234567890"},
		{"123456789012345678901", "12345678901234567..."},
		{strings.Repeat("界", 10), strings.Repeat("界", 10)},
		{strings.Repeat("界", 11), strings.Repeat("界", 8) + "..."},
		{strings.Repeat("e\u0301", 21), strings.Repeat("e\u0301", 17) + "..."},
		{"short\r\n123456789012345678901\rtail", "short\n12345678901234567...\ntail"},
		{"12345678901234567\tend", "12345678901234567..."},
		{"\x1b[31m123456789012345678901\x1b[0m", "\x1b[31m12345678901234567\x1b[0m..."},
	} {
		got := tableCell(tc.input)
		if got != tc.want || !utf8.ValidString(got) {
			t.Fatalf("%q: got %q, want %q", tc.input, got, tc.want)
		}
		for line := range strings.SplitSeq(got, "\n") {
			if text.StringWidthWithoutEscSequences(line) > 20 {
				t.Fatalf("cell exceeds limit: %q", line)
			}
		}
	}
}

// SPEC-0008 REQ-001/002: lists/details share bounded cells; source data and
// non-table JSON sections and pagination metadata remain complete.
func TestREQ002TablePresentationOnly(t *testing.T) {
	const long = "abcdefghijklmnopqrstuvwxyz"
	view := View{Headers: []string{long, "wide"}, Rows: [][]string{{long, strings.Repeat("界", 11)}, {"first\n" + long, "short"}},
		Sections: []Section{{Name: "Result", JSON: []byte(`{"value":"` + long + `"}`)}}, NextCursor: long, ObservedAt: long}
	headers := append([]string{}, view.Headers...)
	rows := [][]string{append([]string{}, view.Rows[0]...), append([]string{}, view.Rows[1]...)}
	var out bytes.Buffer
	if err := view.Write(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ABCDEFGHIJKLMNOPQ...", "abcdefghijklmnopq...", `"value": "` + long + `"`, "Next cursor: " + long, "Observed at: " + long} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	for line := range strings.SplitSeq(out.String(), "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		for _, cell := range strings.Split(line, "|")[1:3] {
			if text.StringWidth(strings.TrimSpace(cell)) > 20 {
				t.Fatalf("rendered cell exceeds width: %q", cell)
			}
		}
	}
	if !reflect.DeepEqual(view.Headers, headers) || !reflect.DeepEqual(view.Rows, rows) {
		t.Fatal("presentation mutated its source")
	}
	out.Reset()
	if err := Details([]string{"ID", long}).Write(&out); err != nil || strings.Contains(out.String(), long) || !strings.Contains(out.String(), "abcdefghijklmnopq...") {
		t.Fatal(out.String(), err)
	}
}
