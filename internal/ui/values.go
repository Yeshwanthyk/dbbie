package ui

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

const maxColumnWidth = 40
const widthSampleRows = 100

func rowValue(row []any, col int) any {
	if col < 0 || col >= len(row) {
		return nil
	}
	return row[col]
}

func fullValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case string:
		return v
	case []byte:
		if !utf8.Valid(v) {
			return "0x" + hex.EncodeToString(v)
		}
		return string(v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprint(v)
	}
}

// Terminal control characters are data, never escape sequences to execute.
func displayValue(text string) string {
	var out strings.Builder
	for _, r := range text {
		switch r {
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if unicode.IsControl(r) {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}

func compactValue(value any, column query.Column) string {
	if value == nil {
		return "∅"
	}
	if b, ok := boolValue(value, column); ok {
		return fmt.Sprint(b)
	}
	text := fullValue(value)
	if tm, ok := timestamp(value, column); ok {
		return tm.Format("2006-01-02 15:04:05")
	}
	if strings.Contains(strings.ToUpper(column.Database), "JSON") {
		var compact bytes.Buffer
		if json.Compact(&compact, []byte(text)) == nil {
			text = compact.String()
		}
	}
	return displayValue(text)
}

func timestamp(value any, column query.Column) (time.Time, bool) {
	if tm, ok := value.(time.Time); ok {
		return tm, true
	}
	text := fullValue(value)
	// Normalized query results use RFC3339, including for driver time.Time.
	if tm, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return tm, true
	}
	if strings.Contains(strings.ToUpper(column.Database), "TIME") {
		for _, layout := range []string{"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999"} {
			if tm, err := time.Parse(layout, text); err == nil {
				return tm, true
			}
		}
	}
	return time.Time{}, false
}

func numericValue(value any, column query.Column) (*big.Rat, bool) {
	if value == nil {
		return nil, false
	}
	kind := reflect.TypeOf(value).Kind()
	isNumber := kind >= reflect.Int && kind <= reflect.Float64
	if !isNumber {
		typ := strings.ToUpper(column.Database)
		for _, name := range []string{"INT", "NUMERIC", "DECIMAL", "FLOAT", "DOUBLE", "REAL", "NUMBER"} {
			isNumber = isNumber || strings.Contains(typ, name)
		}
	}
	if !isNumber {
		return nil, false
	}
	number, ok := new(big.Rat).SetString(fullValue(value))
	return number, ok
}

func boolValue(value any, column query.Column) (bool, bool) {
	if v, ok := value.(bool); ok {
		return v, true
	}
	if strings.Contains(strings.ToUpper(column.Database), "BOOL") {
		switch strings.ToLower(fullValue(value)) {
		case "true", "t", "1":
			return true, true
		case "false", "f", "0":
			return false, true
		}
	}
	return false, false
}

func truncateCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if tview.TaggedStringWidth(tview.Escape(text)) <= width {
		return text
	}
	var out strings.Builder
	used := 0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		w := graphemes.Width()
		if used+w > width-1 {
			break
		}
		out.WriteString(graphemes.Str())
		used += w
	}
	return out.String() + "…"
}

func gridValue(value any, column query.Column, width int) string {
	text := compactValue(value, column)
	if b, ok := boolValue(value, column); ok {
		text = fmt.Sprint(b)
	}
	if strings.EqualFold(column.Database, "UUID") && len(text) == 36 && len(text) > width && width >= 9 {
		return text[:8] + "…"
	}
	return truncateCells(text, width)
}

func columnWidths(result query.Result) []int {
	widths := make([]int, len(result.Columns))
	for col, column := range result.Columns {
		width := max(4, tview.TaggedStringWidth(tview.Escape(displayValue(column.Name))))
		for _, row := range result.Rows[:min(widthSampleRows, len(result.Rows))] {
			width = max(width, tview.TaggedStringWidth(tview.Escape(compactValue(rowValue(row, col), column))))
		}
		widths[col] = min(maxColumnWidth, width)
	}
	return widths
}

func compareValues(a, b any, column query.Column) int {
	if x, ok := numericValue(a, column); ok {
		if y, ok := numericValue(b, column); ok {
			return x.Cmp(y)
		}
	}
	if x, ok := timestamp(a, column); ok {
		if y, ok := timestamp(b, column); ok {
			return x.Compare(y)
		}
	}
	if x, ok := boolValue(a, column); ok {
		if y, ok := boolValue(b, column); ok {
			if x == y {
				return 0
			}
			if x {
				return 1
			}
			return -1
		}
	}
	return strings.Compare(fullValue(a), fullValue(b))
}
