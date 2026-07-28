package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestTablePipedIsTSVWithoutHeader(t *testing.T) {
	var buf bytes.Buffer
	err := Table(&buf, []string{"A", "B"}, [][]string{{"1", "2"}, {"3", "4"}})
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	want := "1\t2\n3\t4\n"
	if buf.String() != want {
		t.Errorf("Table piped = %q, want %q", buf.String(), want)
	}
}

func TestCSVQuoting(t *testing.T) {
	var buf bytes.Buffer
	err := CSV(&buf, []string{"a", "b"}, [][]string{{"plain", `has,comma`}})
	if err != nil {
		t.Fatalf("CSV: %v", err)
	}
	want := "a,b\nplain,\"has,comma\"\n"
	if buf.String() != want {
		t.Errorf("CSV = %q, want %q", buf.String(), want)
	}
}

func TestJSONIndented(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, map[string]int{"n": 1}); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(buf.String(), "\n  \"n\": 1\n") {
		t.Errorf("JSON = %q, want indented output", buf.String())
	}
}

func TestVisibleWidthIgnoresANSI(t *testing.T) {
	styled := "\x1b[32myes\x1b[0m"
	if got := visibleWidth(styled); got != 3 {
		t.Errorf("visibleWidth(%q) = %d, want 3", styled, got)
	}
	if got := visibleWidth("plain"); got != 5 {
		t.Errorf("visibleWidth(plain) = %d, want 5", got)
	}
}
