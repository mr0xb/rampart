// Package output renders results as machine-friendly tables, JSON, or an
// interactive bubbletea table.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// Table writes a plain tab-aligned table. Columns are separated by 2+ spaces,
// values never contain tabs, so the output stays awk/cut friendly.
func Table(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// JSON pretty-prints raw JSON (or marshals any other value) to w.
func JSON(w io.Writer, v any) error {
	var data []byte
	switch t := v.(type) {
	case json.RawMessage:
		data = t
	case []byte:
		data = t
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		data = b
	}
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("null")
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// Duration renders seconds as a compact human duration ("3d4h", "12m").
func Duration(secs int) string {
	if secs <= 0 {
		return "-"
	}
	d := time.Duration(secs) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// YesNo renders a bool the same way everywhere (tables, TUI toggling).
func YesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
