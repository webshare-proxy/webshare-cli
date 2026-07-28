// Package output renders command results appropriately for the environment:
// aligned tables with light color on a terminal, tab-separated values in a
// pipe, and CSV or JSON on request.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
)

// IsTerminal reports whether f is attached to a terminal.
func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// ColorEnabled reports whether ANSI colors should be used on f, honoring
// NO_COLOR (https://no-color.org) and TERM=dumb.
func ColorEnabled(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return IsTerminal(f)
}

// ANSI styles, applied only when enabled.
const (
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiReset = "\x1b[0m"
)

// Styler wraps strings in ANSI escapes when color is enabled.
type Styler struct {
	enabled bool
}

// NewStyler returns a Styler that colors output destined for f.
func NewStyler(f *os.File) Styler {
	return Styler{enabled: ColorEnabled(f)}
}

func (s Styler) wrap(code, text string) string {
	if !s.enabled || text == "" {
		return text
	}
	return code + text + ansiReset
}

// Bold emphasizes text.
func (s Styler) Bold(text string) string { return s.wrap(ansiBold, text) }

// Dim de-emphasizes text.
func (s Styler) Dim(text string) string { return s.wrap(ansiDim, text) }

// Red colors text as an error or negative state.
func (s Styler) Red(text string) string { return s.wrap(ansiRed, text) }

// Green colors text as a positive state.
func (s Styler) Green(text string) string { return s.wrap(ansiGreen, text) }

// ansiPattern matches the ANSI escapes produced by Styler; cells are padded
// by visible width so colored cells never break column alignment.
var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func visibleWidth(s string) int {
	return len([]rune(ansiPattern.ReplaceAllString(s, "")))
}

// Table writes rows under a header. On a terminal the columns are aligned
// and the header is dimmed; in a pipe the cells are tab-separated with no
// header so the output is directly consumable by cut/awk.
func Table(w io.Writer, header []string, rows [][]string) error {
	f, isFile := w.(*os.File)
	if isFile && IsTerminal(f) {
		styler := NewStyler(f)
		if len(rows) == 0 {
			_, err := fmt.Fprintln(w, styler.Dim("(none)"))
			return err
		}
		widths := make([]int, len(header))
		for i, cell := range header {
			widths[i] = visibleWidth(cell)
		}
		for _, row := range rows {
			for i, cell := range row {
				if i < len(widths) && visibleWidth(cell) > widths[i] {
					widths[i] = visibleWidth(cell)
				}
			}
		}
		writeRow := func(cells []string, style func(string) string) error {
			var b strings.Builder
			for i, cell := range cells {
				if i > 0 {
					b.WriteString("  ")
				}
				b.WriteString(style(cell))
				if i < len(cells)-1 {
					b.WriteString(strings.Repeat(" ", widths[i]-visibleWidth(cell)))
				}
			}
			_, err := fmt.Fprintln(w, b.String())
			return err
		}
		if err := writeRow(header, styler.Dim); err != nil {
			return err
		}
		for _, row := range rows {
			if err := writeRow(row, func(s string) string { return s }); err != nil {
				return err
			}
		}
		return nil
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(w, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return nil
}

// KeyValues writes an aligned two-column key/value listing (used for detail
// views such as `account` or `config show`).
func KeyValues(w io.Writer, pairs [][2]string) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	var styler Styler
	if f, ok := w.(*os.File); ok {
		styler = NewStyler(f)
	}
	for _, pair := range pairs {
		fmt.Fprintf(tw, "%s\t%s\n", styler.Dim(pair[0]), pair[1])
	}
	return tw.Flush()
}

// JSON writes v as indented JSON.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// CSV writes rows as RFC 4180 CSV with a header line.
func CSV(w io.Writer, header []string, rows [][]string) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
